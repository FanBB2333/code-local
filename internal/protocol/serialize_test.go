package protocol

import (
	"reflect"
	"testing"
)

func TestVQLRoundTrip(t *testing.T) {
	cases := []uint32{0, 1, 127, 128, 255, 256, 16383, 16384, 0xFFFFFFFF}
	for _, v := range cases {
		w := &Writer{}
		WriteVQL(w, v)
		r := NewReader(w.Bytes())
		got, err := ReadVQL(r)
		if err != nil {
			t.Fatalf("VQL(%d): read error: %v", v, err)
		}
		if got != v {
			t.Fatalf("VQL(%d): got %d", v, got)
		}
	}
}

func TestSerializeUndefined(t *testing.T) {
	w := &Writer{}
	Serialize(w, nil)
	r := NewReader(w.Bytes())
	val, err := Deserialize(r)
	if err != nil {
		t.Fatal(err)
	}
	if val != nil {
		t.Fatalf("expected nil, got %v", val)
	}
}

func TestSerializeString(t *testing.T) {
	w := &Writer{}
	Serialize(w, "hello, 世界")
	r := NewReader(w.Bytes())
	val, err := Deserialize(r)
	if err != nil {
		t.Fatal(err)
	}
	if val != "hello, 世界" {
		t.Fatalf("expected 'hello, 世界', got %v", val)
	}
}

func TestSerializeInt(t *testing.T) {
	cases := []int{0, 1, 100, 127, 128, 1000, 65535}
	for _, v := range cases {
		w := &Writer{}
		Serialize(w, v)
		r := NewReader(w.Bytes())
		got, err := Deserialize(r)
		if err != nil {
			t.Fatalf("int(%d): %v", v, err)
		}
		if got != v {
			t.Fatalf("int(%d): got %v", v, got)
		}
	}
}

func TestSerializeBuffer(t *testing.T) {
	data := []byte{0x00, 0xFF, 0x42, 0x13}
	w := &Writer{}
	Serialize(w, data)
	r := NewReader(w.Bytes())
	got, err := Deserialize(r)
	if err != nil {
		t.Fatal(err)
	}
	gotBytes, ok := got.([]byte)
	if !ok {
		t.Fatalf("expected []byte, got %T", got)
	}
	if !reflect.DeepEqual(gotBytes, data) {
		t.Fatalf("expected %v, got %v", data, gotBytes)
	}
}

func TestSerializeArray(t *testing.T) {
	arr := []interface{}{"hello", 42, nil}
	w := &Writer{}
	Serialize(w, arr)
	r := NewReader(w.Bytes())
	got, err := Deserialize(r)
	if err != nil {
		t.Fatal(err)
	}
	gotArr, ok := got.([]interface{})
	if !ok {
		t.Fatalf("expected []interface{}, got %T", got)
	}
	if len(gotArr) != 3 {
		t.Fatalf("expected len 3, got %d", len(gotArr))
	}
	if gotArr[0] != "hello" {
		t.Fatalf("arr[0]: expected 'hello', got %v", gotArr[0])
	}
	if gotArr[1] != 42 {
		t.Fatalf("arr[1]: expected 42, got %v", gotArr[1])
	}
	if gotArr[2] != nil {
		t.Fatalf("arr[2]: expected nil, got %v", gotArr[2])
	}
}

func TestSerializeObject(t *testing.T) {
	obj := map[string]interface{}{
		"scheme": "file",
		"path":   "/tmp/test",
	}
	w := &Writer{}
	Serialize(w, obj)
	r := NewReader(w.Bytes())
	got, err := Deserialize(r)
	if err != nil {
		t.Fatal(err)
	}
	m, ok := got.(map[string]interface{})
	if !ok {
		t.Fatalf("expected map, got %T", got)
	}
	if m["scheme"] != "file" || m["path"] != "/tmp/test" {
		t.Fatalf("object mismatch: %v", m)
	}
}

// TestSerializeIPCRequest simulates encoding an IPC request header+body
// as VS Code ChannelClient does: header=[type, id, channelName, commandName], body=arg
func TestSerializeIPCRequest(t *testing.T) {
	header := []interface{}{100, 1, "remoteFilesystem", "stat"}
	arg := []interface{}{map[string]interface{}{
		"scheme": "file",
		"path":   "/home/user",
	}}

	w := &Writer{}
	Serialize(w, header)
	Serialize(w, arg)

	data := w.Bytes()
	r := NewReader(data)

	// Deserialize header
	gotHeader, err := Deserialize(r)
	if err != nil {
		t.Fatal(err)
	}
	hdr, ok := gotHeader.([]interface{})
	if !ok {
		t.Fatalf("header: expected array, got %T", gotHeader)
	}
	if len(hdr) != 4 {
		t.Fatalf("header: expected len 4, got %d", len(hdr))
	}
	if hdr[0] != 100 {
		t.Fatalf("header[0]: expected 100, got %v", hdr[0])
	}
	if hdr[2] != "remoteFilesystem" {
		t.Fatalf("header[2]: expected 'remoteFilesystem', got %v", hdr[2])
	}

	// Deserialize body
	gotBody, err := Deserialize(r)
	if err != nil {
		t.Fatal(err)
	}
	bodyArr, ok := gotBody.([]interface{})
	if !ok {
		t.Fatalf("body: expected array, got %T", gotBody)
	}
	if len(bodyArr) != 1 {
		t.Fatalf("body: expected len 1, got %d", len(bodyArr))
	}
}

func TestSerializeIPCResponse(t *testing.T) {
	// Simulate server Initialize response: header=[200], body=undefined
	w := &Writer{}
	Serialize(w, []interface{}{200})
	Serialize(w, nil)

	r := NewReader(w.Bytes())
	header, err := Deserialize(r)
	if err != nil {
		t.Fatal(err)
	}
	hdr := header.([]interface{})
	if hdr[0] != 200 {
		t.Fatalf("expected 200, got %v", hdr[0])
	}

	body, err := Deserialize(r)
	if err != nil {
		t.Fatal(err)
	}
	if body != nil {
		t.Fatalf("expected nil body, got %v", body)
	}
}

func TestSerializeNestedArray(t *testing.T) {
	// Simulate readdir response: [[name, type], ...]
	entries := []interface{}{
		[]interface{}{"file.txt", 1},
		[]interface{}{"src", 2},
	}
	w := &Writer{}
	Serialize(w, entries)
	r := NewReader(w.Bytes())
	got, err := Deserialize(r)
	if err != nil {
		t.Fatal(err)
	}
	arr := got.([]interface{})
	if len(arr) != 2 {
		t.Fatalf("expected 2 entries, got %d", len(arr))
	}
	entry0 := arr[0].([]interface{})
	if entry0[0] != "file.txt" || entry0[1] != 1 {
		t.Fatalf("entry[0]: %v", entry0)
	}
}
