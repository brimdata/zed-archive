package expr

import (
	"math/rand"
	"strconv"
	"testing"

	"github.com/brimdata/zed"
	"github.com/brimdata/zed/order"
	"github.com/brimdata/zed/zson"
)

func BenchmarkSort(b *testing.B) {
	cases := []struct {
		typ   zed.Type
		bytes func() []byte
	}{
		{zed.TypeInt64, func() []byte { return zed.EncodeInt(int64(rand.Uint64())) }},
		{zed.TypeUint64, func() []byte { return zed.EncodeUint(rand.Uint64()) }},
		{zed.TypeString, func() []byte { return strconv.AppendUint(nil, rand.Uint64(), 16) }},
		{zed.TypeDuration, func() []byte { return zed.EncodeInt(int64(rand.Uint64())) }},
		{zed.TypeTime, func() []byte { return zed.EncodeInt(int64(rand.Uint64())) }},
	}
	for _, c := range cases {
		b.Run(zson.FormatType(c.typ), func(b *testing.B) {
			cmp := NewComparator(false, SortEvaluator{&This{}, order.Asc})
			vals := make([]zed.Value, 1048576)
			for i := 0; i < b.N; i++ {
				b.StopTimer()
				for i := range vals {
					vals[i] = zed.NewValue(c.typ, c.bytes())
				}
				b.StartTimer()
				cmp.SortStable(vals)
			}
		})
	}
}
