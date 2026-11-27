package noderings

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"reflect"
	"regexp"
	"strings"
	"sync"
	"testing"
)

// specOperation is what the OpenAPI description says about one operation.
type specOperation struct {
	method, path string
	hasBody      bool
	queryParams  []string
}

func loadSpecOperations(t *testing.T) map[string]specOperation {
	t.Helper()
	raw, err := os.ReadFile("api/openapi.json")
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Paths map[string]map[string]struct {
			OperationID string `json:"operationId"`
			Parameters  []struct {
				Name string `json:"name"`
				In   string `json:"in"`
			} `json:"parameters"`
			RequestBody json.RawMessage `json:"requestBody"`
		} `json:"paths"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatal(err)
	}
	out := map[string]specOperation{}
	for path, ops := range doc.Paths {
		for m, op := range ops {
			so := specOperation{method: strings.ToUpper(m), path: path, hasBody: len(op.RequestBody) > 0}
			for _, p := range op.Parameters {
				if p.In == "query" {
					so.queryParams = append(so.queryParams, p.Name)
				}
			}
			out[op.OperationID] = so
		}
	}
	return out
}

// TestEveryOperation calls every generated method against a fake API and
// checks the HTTP method, the path (with escaping), that a body is sent when
// the API takes one, and that a response decodes.
func TestEveryOperation(t *testing.T) {
	spec := loadSpecOperations(t)
	type seen struct {
		method, path string
		body         []byte
		headers      http.Header
		query        url.Values
	}
	var mu sync.Mutex
	var last seen
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		mu.Lock()
		last = seen{r.Method, r.URL.EscapedPath(), b, r.Header.Clone(), r.URL.Query()}
		mu.Unlock()
		_, _ = w.Write([]byte(`{}`))
	}))
	defer srv.Close()
	c, err := NewClient("tok", WithBaseURL(srv.URL), WithOrganization("org-1"), WithRetries(0, 0, 0))
	if err != nil {
		t.Fatal(err)
	}

	const arg = "a b/c?d"
	placeholder := regexp.MustCompile(`\{[^}]+\}`)
	if len(generatedOperations) < 480 {
		t.Fatalf("only %d operations generated", len(generatedOperations))
	}
	covered := map[string]bool{}
	for _, op := range generatedOperations {
		t.Run(op.service+"."+op.method, func(t *testing.T) {
			want, ok := spec[op.operationID]
			if !ok {
				t.Fatalf("operation %s not in the spec", op.operationID)
			}
			covered[op.operationID] = true
			svc := reflect.ValueOf(c).Elem().FieldByName(op.service)
			if !svc.IsValid() || svc.IsNil() {
				t.Fatalf("client has no service %s", op.service)
			}
			m := svc.MethodByName(op.method)
			if !m.IsValid() {
				t.Fatalf("no method %s.%s", op.service, op.method)
			}
			mt := m.Type()
			args := []reflect.Value{reflect.ValueOf(context.Background())}
			hasBody := false
			for i := 1; i < mt.NumIn(); i++ {
				in := mt.In(i)
				switch {
				case in.Kind() == reflect.String:
					args = append(args, reflect.ValueOf(arg))
				case in.Kind() == reflect.Pointer && strings.HasSuffix(in.Elem().Name(), "Params"):
					p := reflect.New(in.Elem())
					for f := 0; f < in.Elem().NumField(); f++ {
						setNonZero(p.Elem().Field(f))
					}
					if in.Elem().NumField() != len(want.queryParams) {
						t.Errorf("%s has %d fields, the spec %d query parameters", in.Elem().Name(), in.Elem().NumField(), len(want.queryParams))
					}
					args = append(args, p)
				case in.Kind() == reflect.Pointer:
					hasBody = true
					args = append(args, reflect.New(in.Elem()))
				default:
					t.Fatalf("unexpected argument type %s", in)
				}
			}
			outs := m.Call(args)
			if err, _ := outs[1].Interface().(error); err != nil {
				t.Fatalf("call failed: %v", err)
			}
			if outs[0].IsNil() {
				t.Fatal("nil response without an error")
			}
			mu.Lock()
			got := last
			mu.Unlock()
			wantPath := placeholder.ReplaceAllString(want.path, url.PathEscape(arg))
			if got.method != want.method || got.path != wantPath {
				t.Fatalf("sent %s %s, want %s %s", got.method, got.path, want.method, wantPath)
			}
			if hasBody && !want.hasBody {
				t.Fatal("body argument for an operation without a request body")
			}
			if want.hasBody && len(got.body) == 0 {
				t.Fatal("no body sent")
			}
			for _, name := range want.queryParams {
				if _, ok := got.query[name]; !ok {
					t.Errorf("query parameter %q not sent (got %v)", name, got.query)
				}
			}
			if len(want.queryParams) > 0 && len(got.query) == 0 {
				t.Error("no query string sent")
			}
			if got.headers.Get("Authorization") != "Bearer tok" || got.headers.Get("X-Organization-ID") != "org-1" {
				t.Fatalf("auth headers: %v", got.headers)
			}
		})
	}
	// Every operation of the spec is generated, except the excluded areas.
	for id := range spec {
		if covered[id] || strings.HasPrefix(id, "WebhookService_") || strings.HasPrefix(id, "EnforcementService_") {
			continue
		}
		t.Errorf("operation %s has no generated method", id)
	}
}

func setNonZero(f reflect.Value) {
	switch f.Kind() {
	case reflect.String:
		f.SetString("x")
	case reflect.Bool:
		f.SetBool(true)
	case reflect.Int, reflect.Int32, reflect.Int64:
		f.SetInt(7)
	case reflect.Float32, reflect.Float64:
		f.SetFloat(1.5)
	case reflect.Slice:
		s := reflect.MakeSlice(f.Type(), 1, 1)
		setNonZero(s.Index(0))
		f.Set(s)
	case reflect.Pointer:
		p := reflect.New(f.Type().Elem())
		if f.Type().Elem().Kind() != reflect.Struct {
			setNonZero(p.Elem())
		}
		f.Set(p)
	}
}

// iteratorListBody builds a JSON object with a one-element array for every
// slice field of every List* response, so each ...All iterator yields an item.
func iteratorListBody(c *Client) ([]byte, error) {
	obj := map[string]any{}
	cv := reflect.ValueOf(c).Elem()
	if f := cv.FieldByName("services"); f.IsValid() && f.Kind() == reflect.Struct {
		cv = f
	}
	for i := 0; i < cv.NumField(); i++ {
		svc := cv.Field(i)
		if !svc.IsValid() || svc.Kind() != reflect.Pointer || svc.IsNil() {
			continue
		}
		st := svc.Type()
		for j := 0; j < st.NumMethod(); j++ {
			name := st.Method(j).Name
			if !strings.HasPrefix(name, "List") || strings.HasSuffix(name, "All") {
				continue
			}
			m := st.Method(j).Type
			if m.NumOut() < 1 || m.Out(0).Kind() != reflect.Pointer {
				continue
			}
			rt := m.Out(0).Elem()
			if rt.Kind() != reflect.Struct {
				continue
			}
			for k := 0; k < rt.NumField(); k++ {
				f := rt.Field(k)
				if f.Type.Kind() != reflect.Slice {
					continue
				}
				tag := strings.Split(f.Tag.Get("json"), ",")[0]
				if tag == "" || tag == "-" {
					continue
				}
				elem := f.Type.Elem()
				var zero any
				switch elem.Kind() {
				case reflect.String:
					zero = ""
				case reflect.Struct, reflect.Map, reflect.Pointer:
					zero = map[string]any{}
				default:
					zero = reflect.Zero(elem).Interface()
				}
				obj[tag] = []any{zero}
			}
		}
	}
	if len(obj) == 0 {
		return nil, errors.New("no list response fields found")
	}
	return json.Marshal(obj)
}

// TestGeneratedIterators calls every generated ...All method against a fake
// API. Those methods each inline the pager, so statement coverage only
// counts them when they run. Multi-page behaviour and error paths are
// covered in TestIterators and TestIteratorError.
func TestGeneratedIterators(t *testing.T) {
	c, err := NewClient("tok", WithRetries(0, 0, 0))
	if err != nil {
		t.Fatal(err)
	}
	body, err := iteratorListBody(c)
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// A full first page makes the iterator request the next page; an
		// empty later page stops it. That covers the inlined pager loop.
		if r.URL.Query().Get("page") != "" && r.URL.Query().Get("page") != "0" {
			_, _ = w.Write([]byte(`{}`))
			return
		}
		_, _ = w.Write(body)
	}))
	defer srv.Close()
	c, err = NewClient("tok", WithBaseURL(srv.URL), WithRetries(0, 0, 0))
	if err != nil {
		t.Fatal(err)
	}

	cv := reflect.ValueOf(c).Elem()
	// Service pointers live on the embedded services struct.
	if f := cv.FieldByName("services"); f.IsValid() && f.Kind() == reflect.Struct {
		cv = f
	}
	ct := cv.Type()
	n := 0
	for i := 0; i < ct.NumField(); i++ {
		svc := cv.Field(i)
		if !svc.IsValid() || svc.Kind() != reflect.Pointer || svc.IsNil() {
			continue
		}
		st := svc.Type()
		for j := 0; j < st.NumMethod(); j++ {
			name := st.Method(j).Name
			if !strings.HasSuffix(name, "All") {
				continue
			}
			n++
			t.Run(ct.Field(i).Name+"."+name, func(t *testing.T) {
				m := svc.MethodByName(name)
				mt := m.Type()
				args := []reflect.Value{reflect.ValueOf(context.Background())}
				for k := 1; k < mt.NumIn(); k++ {
					in := mt.In(k)
					switch {
					case in.Kind() == reflect.String:
						args = append(args, reflect.ValueOf("id"))
					case in.Kind() == reflect.Pointer:
						pv := reflect.New(in.Elem())
						if f := pv.Elem().FieldByName("PageSize"); f.IsValid() && f.CanSet() {
							f.SetInt(1)
						}
						args = append(args, pv)
					default:
						t.Fatalf("unexpected argument type %s", in)
					}
				}
				outs := m.Call(args)
				if len(outs) != 1 {
					t.Fatalf("got %d return values, want the iterator", len(outs))
				}
				seq := outs[0]
				yieldTyp := seq.Type().In(0)
				yield := reflect.MakeFunc(yieldTyp, func(in []reflect.Value) []reflect.Value {
					if err, _ := in[1].Interface().(error); err != nil {
						t.Errorf("iterator error: %v", err)
					}
					return []reflect.Value{reflect.ValueOf(true)}
				})
				seq.Call([]reflect.Value{yield})
			})
		}
	}
	if n < 60 {
		t.Fatalf("only found %d ...All methods", n)
	}
}
