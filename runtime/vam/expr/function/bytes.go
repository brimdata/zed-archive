package function

import (
	"encoding/base64"
	"encoding/hex"

	"github.com/brimdata/zed"
	"github.com/brimdata/zed/vector"
)

// https://github.com/brimdata/zed/blob/main/docs/language/functions.md#base64
type Base64 struct {
	zctx *zed.Context
}

func (b *Base64) Call(args ...vector.Any) vector.Any {
	val := vector.Under(args[0])
	switch val.Type().ID() {
	case zed.IDBytes:
		var errcnt uint32
		tags := make([]uint32, val.Len())
		out := vector.NewStringEmpty(0, nil)
		for i := uint32(0); i < val.Len(); i++ {
			bytes, null := vector.BytesValue(val, i)
			if null {
				errcnt++
				tags[i] = 1
				continue
			}
			out.Append(base64.StdEncoding.EncodeToString(bytes))
		}
		err := vector.NewStringError(b.zctx, "base64: illegal null argument", errcnt)
		return vector.NewVariant(tags, []vector.Any{out, err})
	case zed.IDString:
		errvals := vector.NewStringEmpty(0, nil)
		tags := make([]uint32, val.Len())
		out := vector.NewBytesEmpty(0, vector.NewBoolEmpty(val.Len(), nil))
		for i := uint32(0); i < val.Len(); i++ {
			s, null := vector.StringValue(val, i)
			if null {
				out.Nulls.Set(i)
			}
			bytes, err := base64.StdEncoding.DecodeString(s)
			if err != nil {
				errvals.Append(s)
				tags[i] = 1
				continue
			}
			out.Append(bytes)
		}
		err := vector.NewWrappedError(b.zctx, "base64: string argument is not base64", errvals)
		return vector.NewVariant(tags, []vector.Any{out, err})
	default:
		return vector.NewWrappedError(b.zctx, "base64: argument must a bytes or string type", val)
	}
}

// https://github.com/brimdata/zed/blob/main/docs/language/functions.md#hex
type Hex struct {
	zctx *zed.Context
}

func (h *Hex) Call(args ...vector.Any) vector.Any {
	val := vector.Under(args[0])
	switch val.Type().ID() {
	case zed.IDBytes:
		var errcnt uint32
		tags := make([]uint32, val.Len())
		out := vector.NewStringEmpty(val.Len(), nil)
		for i := uint32(0); i < val.Len(); i++ {
			bytes, null := vector.BytesValue(val, i)
			if null {
				errcnt++
				tags[i] = 1
				continue
			}
			out.Append(hex.EncodeToString(bytes))
		}
		err := vector.NewStringError(h.zctx, "hex: illegal null argument", errcnt)
		return vector.NewVariant(tags, []vector.Any{out, err})
	case zed.IDString:
		errvals := vector.NewStringEmpty(0, nil)
		tags := make([]uint32, val.Len())
		out := vector.NewBytesEmpty(0, vector.NewBoolEmpty(val.Len(), nil))
		for i := uint32(0); i < val.Len(); i++ {
			s, null := vector.StringValue(val, i)
			if null {
				out.Nulls.Set(i)
			}
			bytes, err := hex.DecodeString(s)
			if err != nil {
				errvals.Append(s)
				tags[i] = 1
				continue
			}
			out.Append(bytes)
		}
		err := vector.NewWrappedError(h.zctx, "hex: string argument is not hexidecimal", errvals)
		return vector.NewVariant(tags, []vector.Any{out, err})
	default:
		return vector.NewWrappedError(h.zctx, "hex: argument must a bytes or string type", val)
	}
}
