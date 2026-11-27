package noderings

import (
	"crypto/rand"
	"encoding/json"
	"fmt"
	"net/url"
	"reflect"
	"strconv"
	"time"
)

// Ptr returns a pointer to v, for optional fields such as *bool:
//
//	&noderings.DeleteDNSZoneParams{Force: noderings.Ptr(true)}
func Ptr[T any](v T) *T { return &v }

// NewIdempotencyKey returns a random UUID (version 4). Creates that take an
// idempotency key get one automatically; set your own when you need to retry
// a create after your program restarts.
func NewIdempotencyKey() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic("noderings: no randomness: " + err.Error())
	}
	b[6] = b[6]&0x0f | 0x40
	b[8] = b[8]&0x3f | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}

// Int64s is a list of 64-bit integers, sent by the API as JSON strings.
type Int64s []int64

// MarshalJSON implements json.Marshaler.
func (s Int64s) MarshalJSON() ([]byte, error) {
	out := make([]string, len(s))
	for i, v := range s {
		out[i] = strconv.FormatInt(v, 10)
	}
	return json.Marshal(out)
}

// UnmarshalJSON implements json.Unmarshaler. It accepts strings and numbers.
func (s *Int64s) UnmarshalJSON(b []byte) error {
	var raw []json.RawMessage
	if err := json.Unmarshal(b, &raw); err != nil {
		return err
	}
	out := make(Int64s, 0, len(raw))
	for _, r := range raw {
		var str string
		if json.Unmarshal(r, &str) == nil {
			v, err := strconv.ParseInt(str, 10, 64)
			if err != nil {
				return err
			}
			out = append(out, v)
			continue
		}
		var v int64
		if err := json.Unmarshal(r, &v); err != nil {
			return err
		}
		out = append(out, v)
	}
	*s = out
	return nil
}

// addQuery adds a query parameter, skipping zero values (the API treats a
// missing parameter and its zero value the same). Pointers are sent when set.
func addQuery(q url.Values, name string, v any) {
	rv := reflect.ValueOf(v)
	if !rv.IsValid() {
		return
	}
	if rv.Kind() == reflect.Pointer {
		if rv.IsNil() {
			return
		}
		rv = rv.Elem()
		if t, ok := rv.Interface().(time.Time); ok {
			q.Add(name, t.UTC().Format(time.RFC3339Nano))
			return
		}
		q.Add(name, formatScalar(rv))
		return
	}
	switch rv.Kind() {
	case reflect.Slice:
		for i := 0; i < rv.Len(); i++ {
			q.Add(name, formatScalar(rv.Index(i)))
		}
	default:
		if rv.IsZero() {
			return
		}
		q.Add(name, formatScalar(rv))
	}
}

func formatScalar(v reflect.Value) string {
	switch v.Kind() {
	case reflect.String:
		return v.String()
	case reflect.Bool:
		return strconv.FormatBool(v.Bool())
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return strconv.FormatInt(v.Int(), 10)
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return strconv.FormatUint(v.Uint(), 10)
	case reflect.Float32, reflect.Float64:
		return strconv.FormatFloat(v.Float(), 'f', -1, 64)
	}
	return fmt.Sprint(v.Interface())
}
