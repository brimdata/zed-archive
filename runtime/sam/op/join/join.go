package join

import (
	"context"
	"fmt"
	"sync"

	"github.com/brimdata/zed"
	"github.com/brimdata/zed/order"
	"github.com/brimdata/zed/runtime"
	"github.com/brimdata/zed/runtime/sam/expr"
	"github.com/brimdata/zed/runtime/sam/op/sort"
	"github.com/brimdata/zed/zbuf"
	"github.com/brimdata/zed/zio"
)

type Op struct {
	rctx        *runtime.Context
	anti        bool
	inner       bool
	ctx         context.Context
	cancel      context.CancelFunc
	once        sync.Once
	left        *puller
	right       *zio.Peeker
	getLeftKey  expr.Evaluator
	getRightKey expr.Evaluator
	resetter    expr.Resetter
	compare     expr.CompareFn
	cutter      *expr.Cutter
	joinKey     *zed.Value
	joinSet     []zed.Value
	types       map[int]map[int]*zed.TypeRecord
}

func New(rctx *runtime.Context, anti, inner bool, left, right zbuf.Puller, leftKey, rightKey expr.Evaluator,
	leftDir, rightDir order.Direction, lhs []*expr.Lval, rhs []expr.Evaluator, resetter expr.Resetter) (*Op, error) {
	var o order.Which
	switch {
	case leftDir != order.Unknown:
		o = leftDir == order.Down
	case rightDir != order.Unknown:
		o = rightDir == order.Down
	}
	var err error
	// Add sorts if needed.
	if !leftDir.HasOrder(o) {
		s := expr.NewSortEvaluator(leftKey, o)
		left, err = sort.New(rctx, left, []expr.SortEvaluator{s}, false, false, resetter)
		if err != nil {
			return nil, err
		}
	}
	if !rightDir.HasOrder(o) {
		s := expr.NewSortEvaluator(rightKey, o)
		right, err = sort.New(rctx, right, []expr.SortEvaluator{s}, false, false, resetter)
		if err != nil {
			return nil, err
		}
	}
	ctx, cancel := context.WithCancel(rctx.Context)
	return &Op{
		rctx:        rctx,
		anti:        anti,
		inner:       inner,
		ctx:         ctx,
		cancel:      cancel,
		getLeftKey:  leftKey,
		getRightKey: rightKey,
		left:        newPuller(left, ctx),
		right:       zio.NewPeeker(newPuller(right, ctx)),
		resetter:    resetter,
		compare:     expr.NewValueCompareFn(o, true),
		cutter:      expr.NewCutter(rctx.Zctx, lhs, rhs),
		types:       make(map[int]map[int]*zed.TypeRecord),
	}, nil
}

// Pull implements the merge logic for returning data from the upstreams.
func (o *Op) Pull(done bool) (zbuf.Batch, error) {
	// XXX see issue #3437 regarding done protocol.
	o.once.Do(func() {
		go o.left.run()
		go o.right.Reader.(*puller).run()
	})
	var out []zed.Value
	// See #3366
	ectx := expr.NewContext()
	for {
		leftRec, err := o.left.Read()
		if err != nil {
			return nil, err
		}
		if leftRec == nil {
			if len(out) == 0 {
				o.resetter.Reset()
				return nil, nil
			}
			//XXX See issue #3427.
			return zbuf.NewArray(out), nil
		}
		key := o.getLeftKey.Eval(ectx, *leftRec)
		if key.IsMissing() {
			// If the left key isn't present (which is not a thing
			// in a sql join), then drop the record and return only
			// left records that can eval the key expression.
			continue
		}
		rightRecs, err := o.getJoinSet(key)
		if err != nil {
			return nil, err
		}
		if rightRecs == nil {
			// Nothing to add to the left join.
			// Accumulate this record for an outer join.
			if !o.inner {
				out = append(out, leftRec.Copy())
			}
			continue
		}
		if o.anti {
			continue
		}
		// For every record on the right with a key matching
		// this left record, generate a joined record.
		// XXX This loop could be more efficient if we had CutAppend
		// and built the record in a re-usable buffer, then allocated
		// a right-sized output buffer for the record body and copied
		// the two inputs into the output buffer.  Even better, these
		// output buffers could come from a large buffer that implements
		// Batch and lives in a pool so the downstream user can
		// release the batch with and bypass GC.
		for _, rightRec := range rightRecs {
			cutRec := o.cutter.Eval(ectx, rightRec)
			rec, err := o.splice(*leftRec, cutRec)
			if err != nil {
				return nil, err
			}
			out = append(out, rec)
		}
	}
}

func (o *Op) getJoinSet(leftKey zed.Value) ([]zed.Value, error) {
	if o.joinKey != nil && o.compare(leftKey, *o.joinKey) == 0 {
		return o.joinSet, nil
	}
	// See #3366
	ectx := expr.NewContext()
	for {
		rec, err := o.right.Peek()
		if err != nil || rec == nil {
			return nil, err
		}
		rightKey := o.getRightKey.Eval(ectx, *rec)
		if rightKey.IsMissing() {
			o.right.Read()
			continue
		}
		cmp := o.compare(leftKey, rightKey)
		if cmp == 0 {
			// Copy leftKey.Bytes since it might get reused.
			if o.joinKey == nil {
				o.joinKey = leftKey.Copy().Ptr()
			} else {
				o.joinKey.CopyFrom(leftKey)
			}
			o.joinSet, err = o.readJoinSet(o.joinKey)
			return o.joinSet, err
		}
		if cmp < 0 {
			// If the left key is smaller than the next eligible
			// join key, then there is nothing to join for this
			// record.
			return nil, nil
		}
		// Discard the peeked-at record and keep looking for
		// a righthand key that either matches or exceeds the
		// lefthand key.
		o.right.Read()
	}
}

// fillJoinSet is called when a join key has been found that matches
// the current lefthand key.  It returns the all the subsequent records
// from the righthand stream that match this key.
func (o *Op) readJoinSet(joinKey *zed.Value) ([]zed.Value, error) {
	var recs []zed.Value
	// See #3366
	ectx := expr.NewContext()
	for {
		rec, err := o.right.Peek()
		if err != nil {
			return nil, err
		}
		if rec == nil {
			return recs, nil
		}
		key := o.getRightKey.Eval(ectx, *rec)
		if key.IsMissing() {
			o.right.Read()
			continue
		}
		if o.compare(key, *joinKey) != 0 {
			return recs, nil
		}
		recs = append(recs, rec.Copy())
		o.right.Read()
	}
}

func (o *Op) lookupType(left, right *zed.TypeRecord) *zed.TypeRecord {
	if table, ok := o.types[left.ID()]; ok {
		return table[right.ID()]
	}
	return nil
}

func (o *Op) enterType(combined, left, right *zed.TypeRecord) {
	id := left.ID()
	table := o.types[id]
	if table == nil {
		table = make(map[int]*zed.TypeRecord)
		o.types[id] = table
	}
	table[right.ID()] = combined
}

func (o *Op) buildType(left, right *zed.TypeRecord) (*zed.TypeRecord, error) {
	fields := make([]zed.Field, 0, len(left.Fields)+len(right.Fields))
	fields = append(fields, left.Fields...)
	for _, f := range right.Fields {
		name := f.Name
		for k := 2; left.HasField(name); k++ {
			name = fmt.Sprintf("%s_%d", f.Name, k)
		}
		fields = append(fields, zed.NewField(name, f.Type))
	}
	return o.rctx.Zctx.LookupTypeRecord(fields)
}

func (o *Op) combinedType(left, right *zed.TypeRecord) (*zed.TypeRecord, error) {
	if typ := o.lookupType(left, right); typ != nil {
		return typ, nil
	}
	typ, err := o.buildType(left, right)
	if err != nil {
		return nil, err
	}
	o.enterType(typ, left, right)
	return typ, nil
}

func (o *Op) splice(left, right zed.Value) (zed.Value, error) {
	left = left.Under()
	right = right.Under()
	typ, err := o.combinedType(zed.TypeRecordOf(left.Type()), zed.TypeRecordOf(right.Type()))
	if err != nil {
		return zed.Null, err
	}
	n := len(left.Bytes())
	bytes := make([]byte, n+len(right.Bytes()))
	copy(bytes, left.Bytes())
	copy(bytes[n:], right.Bytes())
	return zed.NewValue(typ, bytes), nil
}
