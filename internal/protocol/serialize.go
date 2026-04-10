package protocol

import (
	"encoding/json"
	"fmt"
	"math"
)

// VS Code IPC binary serialization format.
// Each value is prefixed with a DataType byte, then type-specific encoding.
// Integers and lengths use Variable-Length Quantity (VQL) encoding.

type DataType byte

const (
	DTUndefined DataType = 0
	DTString    DataType = 1
	DTBuffer    DataType = 2
	DTVSBuffer  DataType = 3
	DTArray     DataType = 4
	DTObject    DataType = 5
	DTInt       DataType = 6
)

// Writer accumulates bytes for serialization.
type Writer struct {
	buf []byte
}

func (w *Writer) WriteByte(b byte) {
	w.buf = append(w.buf, b)
}

func (w *Writer) Write(p []byte) {
	w.buf = append(w.buf, p...)
}

func (w *Writer) Bytes() []byte {
	return w.buf
}

// Reader reads bytes for deserialization.
type Reader struct {
	buf []byte
	pos int
}

func NewReader(data []byte) *Reader {
	return &Reader{buf: data}
}

func (r *Reader) ReadByte() (byte, error) {
	if r.pos >= len(r.buf) {
		return 0, fmt.Errorf("read past end")
	}
	b := r.buf[r.pos]
	r.pos++
	return b, nil
}

func (r *Reader) ReadN(n int) ([]byte, error) {
	if r.pos+n > len(r.buf) {
		return nil, fmt.Errorf("read past end: need %d, have %d", n, len(r.buf)-r.pos)
	}
	data := r.buf[r.pos : r.pos+n]
	r.pos += n
	return data, nil
}

func (r *Reader) Remaining() int {
	return len(r.buf) - r.pos
}

// WriteVQL writes a uint32 in Variable-Length Quantity encoding.
func WriteVQL(w *Writer, value uint32) {
	if value == 0 {
		w.WriteByte(0)
		return
	}
	for value > 0 {
		b := byte(value & 0x7F)
		value >>= 7
		if value > 0 {
			b |= 0x80
		}
		w.WriteByte(b)
	}
}

// ReadVQL reads a VQL-encoded uint32.
func ReadVQL(r *Reader) (uint32, error) {
	var value uint32
	for n := uint(0); ; n += 7 {
		b, err := r.ReadByte()
		if err != nil {
			return 0, err
		}
		value |= uint32(b&0x7F) << n
		if b&0x80 == 0 {
			return value, nil
		}
		if n > 28 {
			return 0, fmt.Errorf("VQL overflow")
		}
	}
}

// Serialize writes a Go value in VS Code's binary IPC format.
func Serialize(w *Writer, data interface{}) {
	if data == nil {
		w.WriteByte(byte(DTUndefined))
		return
	}
	switch v := data.(type) {
	case string:
		w.WriteByte(byte(DTString))
		b := []byte(v)
		WriteVQL(w, uint32(len(b)))
		w.Write(b)
	case []byte:
		w.WriteByte(byte(DTVSBuffer))
		WriteVQL(w, uint32(len(v)))
		w.Write(v)
	case int:
		w.WriteByte(byte(DTInt))
		WriteVQL(w, uint32(v))
	case int32:
		w.WriteByte(byte(DTInt))
		WriteVQL(w, uint32(v))
	case uint32:
		w.WriteByte(byte(DTInt))
		WriteVQL(w, v)
	case float64:
		if v == math.Trunc(v) && v >= 0 && v <= math.MaxUint32 {
			w.WriteByte(byte(DTInt))
			WriteVQL(w, uint32(v))
		} else {
			// Fallback to Object (JSON) for non-integer floats
			serializeAsObject(w, v)
		}
	case []interface{}:
		w.WriteByte(byte(DTArray))
		WriteVQL(w, uint32(len(v)))
		for _, el := range v {
			Serialize(w, el)
		}
	default:
		serializeAsObject(w, v)
	}
}

func serializeAsObject(w *Writer, v interface{}) {
	b, _ := json.Marshal(v)
	w.WriteByte(byte(DTObject))
	WriteVQL(w, uint32(len(b)))
	w.Write(b)
}

// Deserialize reads a value from VS Code's binary IPC format.
func Deserialize(r *Reader) (interface{}, error) {
	typeByte, err := r.ReadByte()
	if err != nil {
		return nil, err
	}
	dt := DataType(typeByte)

	switch dt {
	case DTUndefined:
		return nil, nil

	case DTString:
		length, err := ReadVQL(r)
		if err != nil {
			return nil, err
		}
		data, err := r.ReadN(int(length))
		if err != nil {
			return nil, err
		}
		return string(data), nil

	case DTBuffer, DTVSBuffer:
		length, err := ReadVQL(r)
		if err != nil {
			return nil, err
		}
		data, err := r.ReadN(int(length))
		if err != nil {
			return nil, err
		}
		// Return a copy to avoid aliasing
		result := make([]byte, len(data))
		copy(result, data)
		return result, nil

	case DTArray:
		length, err := ReadVQL(r)
		if err != nil {
			return nil, err
		}
		result := make([]interface{}, length)
		for i := uint32(0); i < length; i++ {
			el, err := Deserialize(r)
			if err != nil {
				return nil, fmt.Errorf("array[%d]: %w", i, err)
			}
			result[i] = el
		}
		return result, nil

	case DTObject:
		length, err := ReadVQL(r)
		if err != nil {
			return nil, err
		}
		data, err := r.ReadN(int(length))
		if err != nil {
			return nil, err
		}
		var result interface{}
		if err := json.Unmarshal(data, &result); err != nil {
			return nil, fmt.Errorf("json unmarshal: %w", err)
		}
		return result, nil

	case DTInt:
		val, err := ReadVQL(r)
		if err != nil {
			return nil, err
		}
		return int(val), nil

	default:
		return nil, fmt.Errorf("unknown data type: %d", dt)
	}
}
