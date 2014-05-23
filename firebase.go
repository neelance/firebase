package firebase

import (
	"bytes"
	"encoding/json"
	"fmt"
	"github.com/neelance/eventsource/client"
	"io"
	"net/http"
	"path"
	"reflect"
	"sort"
	"strings"
)

type Ref struct {
	App   string
	Path  string
	cache interface{}
}

type Watch struct {
	client         *client.Client
	bufferedChange *client.Event
}

func New(app string) *Ref {
	return &Ref{
		App:  app,
		Path: "",
	}
}

func (ref *Ref) Child(key string) *Ref {
	return &Ref{
		App:  ref.App,
		Path: path.Join(ref.Path, key),
	}
}

func (ref *Ref) url() string {
	return fmt.Sprintf("https://%s.firebaseio.com/%s.json", ref.App, ref.Path)
}

func (ref *Ref) Get(v interface{}) error {
	req, err := http.NewRequest("GET", ref.url(), nil)
	if err != nil {
		return err
	}
	req.Close = true
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	return json.NewDecoder(resp.Body).Decode(v)
}

func (ref *Ref) Put(v interface{}) error {
	return ref.request("PUT", v)
}

func (ref *Ref) Post(v interface{}) error {
	return ref.request("POST", v)
}

func (ref *Ref) Patch(v interface{}) error {
	return ref.request("PATCH", v)
}

func (ref *Ref) Delete() error {
	return ref.request("DELETE", nil)
}

func (ref *Ref) request(method string, v interface{}) error {
	var body io.Reader
	if v != nil {
		data, err := json.Marshal(v)
		if err != nil {
			return err
		}
		body = bytes.NewReader(data)
	}
	req, err := http.NewRequest(method, ref.url(), body)
	if err != nil {
		return err
	}
	req.Close = true
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	resp.Body.Close()
	return nil
}

func (ref *Ref) Watch() (*Watch, error) {
	client, err := client.New(ref.url())
	if err != nil {
		return nil, err
	}
	return &Watch{client: client}, nil
}

func (w *Watch) WaitForChange() error {
	if w.bufferedChange != nil {
		return nil
	}
	for {
		e, ok := <-w.client.Stream
		if !ok {
			if w.client.Err != nil {
				return w.client.Err
			}
			return io.EOF
		}
		if e.Type == "keep-alive" {
			continue
		}
		w.bufferedChange = &e
		return nil
	}
}

func (w *Watch) ApplyChanges(v interface{}) error {
	if w.bufferedChange != nil {
		if err := processChange(v, w.bufferedChange); err != nil {
			w.bufferedChange = nil
			return err
		}
		w.bufferedChange = nil
	}
	for {
		select {
		case e, ok := <-w.client.Stream:
			if !ok {
				if w.client.Err != nil {
					return w.client.Err
				}
				return io.EOF
			}
			if err := processChange(v, &e); err != nil {
				return err
			}
		default:
			return nil
		}
	}
}

func processChange(v interface{}, e *client.Event) error {
	switch e.Type {
	case "put":
		var change struct {
			Path string
			Data json.RawMessage
		}
		if err := json.Unmarshal(e.Data, &change); err != nil {
			return err
		}
		if change.Path == "/" {
			return json.Unmarshal(change.Data, v)
		}
		keys := strings.Split(change.Path, "/")[1:]
		return applyPatch(v, keys[:len(keys)-1], map[string]json.RawMessage{keys[len(keys)-1]: change.Data})

	case "patch":
		var change struct {
			Path string
			Data map[string]json.RawMessage
		}
		if err := json.Unmarshal(e.Data, &change); err != nil {
			return err
		}
		return applyPatch(v, strings.Split(change.Path, "/")[1:], change.Data)

	default:
		return nil
	}
}

func applyPatch(v interface{}, keys []string, fields map[string]json.RawMessage) error {
	target := reflect.ValueOf(v).Elem()
	for {
		if target.Type().Kind() == reflect.Interface {
			if target.IsNil() {
				target.Set(reflect.ValueOf(make(map[string]interface{})))
			}
			target = target.Elem()
		}
		for target.Type().Kind() == reflect.Ptr {
			if target.IsNil() {
				target.Set(reflect.New(target.Type().Elem()))
			}
			target = target.Elem()
		}
		if target.Type().Kind() == reflect.Map && target.IsNil() {
			target.Set(reflect.MakeMap(target.Type()))
		}

		if len(keys) == 0 {
			break
		}
		key := keys[0]
		keys = keys[1:]

		switch target.Type().Kind() {
		case reflect.Map:
			value := target.MapIndex(reflect.ValueOf(key))
			if !value.IsValid() {
				value = reflect.ValueOf(make(map[string]interface{}))
				target.SetMapIndex(reflect.ValueOf(key), value)
			}
			target = value
		case reflect.Struct:
			target = fieldByKey(target, key)
		default:
			return fmt.Errorf("invalid type: %s", target.Type())
		}
	}

	for field, data := range fields {
		if bytes.Equal(data, []byte("null")) {
			switch target.Type().Kind() {
			case reflect.Map:
				target.SetMapIndex(reflect.ValueOf(field), reflect.Value{})
			case reflect.Struct:
				f := fieldByKey(target, field)
				f.Set(reflect.Zero(f.Type()))
			default:
				return fmt.Errorf("invalid type: %s", target.Type())
			}
			continue
		}

		switch target.Type().Kind() {
		case reflect.Map:
			ptr := reflect.New(target.Type().Elem())
			if err := json.Unmarshal(data, ptr.Interface()); err != nil {
				return err
			}
			val := ptr.Elem()
			target.SetMapIndex(reflect.ValueOf(field), val)
		case reflect.Struct:
			f := fieldByKey(target, field)
			if !f.IsValid() {
				continue // skip field
			}
			if !f.CanAddr() {
				return fmt.Errorf("can not write field of %s, use pointer to struct instead", target.Type())
			}
			if err := json.Unmarshal(data, f.Addr().Interface()); err != nil {
				return err
			}
		default:
			return fmt.Errorf("invalid type: %s", target.Type())
		}
	}

	return nil
}

func fieldByKey(v reflect.Value, name string) reflect.Value {
	t := v.Type()
	for i := 0; i < t.NumField(); i++ {
		f := t.Field(i)
		if f.Name == name || strings.ToLower(f.Name) == name {
			return v.Field(i)
		}
	}
	return reflect.Value{}
}

func OrderedKeys(data interface{}) []string {
	keyValues := reflect.ValueOf(data).MapKeys()
	keys := make([]string, 0, len(keyValues))
	for _, value := range keyValues {
		keys = append(keys, value.String())
	}
	sort.Strings(keys)
	return keys
}
