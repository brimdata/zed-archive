package vector

import (
	"github.com/brimdata/zed/zcode"
)

type View struct {
	Any
	Index []uint32
}

var _ Any = (*View)(nil)

func NewView(index []uint32, val Any) Any {
	switch val := val.(type) {
	case *Dict:
		index2 := make([]byte, len(index))
		nulls := NewBoolEmpty(uint32(len(index)), nil)
		for k, idx := range index {
			if val.Nulls.Value(idx) {
				nulls.Set(uint32(k))
			}
			index2[k] = val.Index[idx]
		}
		return NewDict(val.Any, index2, nil, nulls)
	case *Union:
		tags, values := viewForUnionOrVariant(index, val.Tags, val.TagMap.Forward, val.Values)
		return NewUnion(val.Typ, tags, values, nil)
	case *Variant:
		return NewVariant(viewForUnionOrVariant(index, val.Tags, val.TagMap.Forward, val.Values))
	case *View:
		index2 := make([]uint32, len(index))
		for k, idx := range index {
			index2[k] = uint32(val.Index[idx])
		}
		return &View{val.Any, index2}
	}
	return &View{val, index}
}

func viewForUnionOrVariant(index, tags, forward []uint32, values []Any) ([]uint32, []Any) {
	indexes := make([][]uint32, len(values))
	resultTags := make([]uint32, len(index))
	for k, index := range index {
		tag := tags[index]
		indexes[tag] = append(indexes[tag], forward[index])
		resultTags[k] = tag
	}
	results := make([]Any, len(values))
	for k := range results {
		results[k] = NewView(indexes[k], values[k])
	}
	return resultTags, results
}

func (v *View) Len() uint32 {
	return uint32(len(v.Index))
}

func (v *View) Serialize(b *zcode.Builder, slot uint32) {
	v.Any.Serialize(b, v.Index[slot])
}
