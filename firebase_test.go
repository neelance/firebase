package firebase

import (
	"encoding/json"
	"github.com/neelance/eventsource/client"
	"io"
	"reflect"
	"testing"
)

type DataEmbedded struct {
	Outer struct {
		Inner struct {
			A int `json:"a"`
			B int `json:"b"`
			C int `json:"c,omitempty"`
		} `json:"inner"`
	} `json:"outer"`
}

type DataWithPointers struct {
	Outer *struct {
		Inner *struct {
			A int `json:"a"`
			B int `json:"b"`
			C int `json:"c,omitempty"`
		} `json:"inner"`
	} `json:"outer"`
}

func TestPutRoot(t *testing.T) {
	testWatch(t, []client.Event{
		client.Event{Type: "put", Data: []byte(`{ "path": "/", "data": { "outer": { "inner": { "a": 1, "b": 1 } } } }`)},
	})
}

func TestPutNewKey(t *testing.T) {
	testWatch(t, []client.Event{
		client.Event{Type: "put", Data: []byte(`{ "path": "/outer", "data": { "inner": { "a": 1, "b": 1 } } }`)},
	})
}

func TestPutNewKeyToNonExisting(t *testing.T) {
	testWatch(t, []client.Event{
		client.Event{Type: "put", Data: []byte(`{ "path": "/", "data": { } }`)},
		client.Event{Type: "put", Data: []byte(`{ "path": "/outer/inner", "data": { "a": 1, "b": 1 } }`)},
	})
}

func TestPutExistingKey(t *testing.T) {
	testWatch(t, []client.Event{
		client.Event{Type: "put", Data: []byte(`{ "path": "/", "data": { "outer": { "inner": { "a": 0, "b": 1 } } } }`)},
		client.Event{Type: "put", Data: []byte(`{ "path": "/outer/inner/a", "data": 1 }`)},
	})
}

func TestRemoveKey(t *testing.T) {
	testWatch(t, []client.Event{
		client.Event{Type: "put", Data: []byte(`{ "path": "/", "data": { "outer": { "inner": { "a": 1, "b": 1, "c": 1 } } } }`)},
		client.Event{Type: "put", Data: []byte(`{ "path": "/outer/inner/c", "data": null }`)},
	})
}

func TestPatch(t *testing.T) {
	testWatch(t, []client.Event{
		client.Event{Type: "put", Data: []byte(`{ "path": "/", "data": { "outer": { "inner": { "a": 1 } } } }`)},
		client.Event{Type: "patch", Data: []byte(`{ "path": "/outer/inner", "data": { "b": 1 } }`)},
	})
}

func testWatch(t *testing.T, events []client.Event) {
	types := []reflect.Type{
		// reflect.TypeOf((*interface{})(nil)).Elem(),
		reflect.TypeOf(map[string]interface{}{}),
		// reflect.TypeOf(DataEmbedded{}),
		// reflect.TypeOf(DataWithPointers{}),
	}

	for _, typ := range types {
		stream := make(chan client.Event, len(events))
		for _, e := range events {
			stream <- e
		}
		close(stream)

		watch := Watch{&client.Client{Stream: stream}}
		data := reflect.New(typ)
		for {
			err := watch.ApplyChanges(data.Interface())
			if err != nil {
				if err == io.EOF {
					break
				}
				t.Error(err)
			}
		}

		got, _ := json.Marshal(data.Elem().Interface())
		expected := `{"outer":{"inner":{"a":1,"b":1}}}`

		if string(got) != expected {
			t.Errorf("\nexpected\n\t%#v\ngot\n\t%#v", expected, string(got))
		}
	}
}
