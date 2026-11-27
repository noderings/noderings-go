package noderings

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// fakeAPI answers with h, which gets the request number (1 for the first).
func fakeAPI(t *testing.T, h func(w http.ResponseWriter, r *http.Request, n int32)) (*Client, *atomic.Int32) {
	t.Helper()
	var n atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h(w, r, n.Add(1))
	}))
	t.Cleanup(srv.Close)
	c, err := NewClient("tok", WithBaseURL(srv.URL), WithRetries(3, time.Millisecond, 5*time.Millisecond))
	if err != nil {
		t.Fatal(err)
	}
	return c, &n
}

func writeJSON(w http.ResponseWriter, status int, body string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, _ = io.WriteString(w, body)
}

func TestNewClientOptions(t *testing.T) {
	if _, err := NewClient(" "); err == nil {
		t.Error("empty token accepted")
	}
	if _, err := NewClient("t", WithBaseURL("not a url")); err == nil {
		t.Error("bad base URL accepted")
	}
	if _, err := NewClient("t", WithRetries(-1, 0, 0)); err == nil {
		t.Error("negative retries accepted")
	}
	if _, err := NewClient("t", WithHTTPClient(nil)); err == nil {
		t.Error("nil HTTP client accepted")
	}
	c, err := NewClient("t")
	if err != nil || c.baseURL.String() != DefaultBaseURL || c.VirtualMachine == nil || c.DNSRecord == nil {
		t.Fatalf("defaults: %v %v", c, err)
	}
}

func TestHeadersAndBaseURLPath(t *testing.T) {
	var got *http.Request
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = r.Clone(context.Background())
		writeJSON(w, 200, `{"virtualMachines":[],"totalCount":0}`)
	}))
	defer srv.Close()
	c, _ := NewClient("secret", WithBaseURL(srv.URL+"/proxy/"), WithOrganization(" org-9 "), WithUserAgent("my-app/1.0"))
	if _, err := c.VirtualMachine.ListVirtualMachines(context.Background(), &ListVirtualMachinesParams{PageSize: 10, FilterOperationStatus: OperationStatusStarted}); err != nil {
		t.Fatal(err)
	}
	if got.URL.Path != "/proxy/v1/virtual-machines" {
		t.Errorf("path %s", got.URL.Path)
	}
	if got.Header.Get("Authorization") != "Bearer secret" || got.Header.Get("X-Organization-ID") != "org-9" {
		t.Errorf("headers %v", got.Header)
	}
	if ua := got.Header.Get("User-Agent"); ua != "my-app/1.0 noderings-go/"+Version {
		t.Errorf("user agent %q", ua)
	}
	if q := got.URL.Query(); q.Get("pageSize") != "10" || q.Get("filterOperationStatus") != "OPERATION_STATUS_STARTED" || q.Has("page") {
		t.Errorf("query %v (page 0 must be left out)", q)
	}
	if got.Header.Get("Content-Type") != "" {
		t.Error("Content-Type set on a GET without body")
	}
}

func TestNoOrganizationHeaderByDefault(t *testing.T) {
	c, _ := fakeAPI(t, func(w http.ResponseWriter, r *http.Request, n int32) {
		if _, ok := r.Header["X-Organization-Id"]; ok {
			t.Error("X-Organization-ID sent without WithOrganization")
		}
		writeJSON(w, 200, `{}`)
	})
	if _, err := c.Organization.GetOrganization(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func TestDecodesInt64StringsEnumsAndTimes(t *testing.T) {
	c, _ := fakeAPI(t, func(w http.ResponseWriter, r *http.Request, n int32) {
		writeJSON(w, 200, `{"backups":[{"id":"b1","sizeBytes":"21474836480","createdAt":"2026-11-27T00:50:00.5Z","backupStatus":"BACKUP_STATUS_ACTIVE"}],"totalCount":1}`)
	})
	out, err := c.Backup.ListBackups(context.Background(), "vm-1", nil)
	if err != nil {
		t.Fatal(err)
	}
	b := out.Backups[0]
	if b.SizeBytes != 21474836480 || b.BackupStatus != BackupStatusActive {
		t.Errorf("backup %+v", b)
	}
	if b.CreatedAt == nil || !b.CreatedAt.Equal(time.Date(2026, 11, 27, 0, 50, 0, 5e8, time.UTC)) {
		t.Errorf("createdAt %v", b.CreatedAt)
	}
}

func TestEncodesBodiesAsTheAPIExpects(t *testing.T) {
	var body map[string]any
	c, _ := fakeAPI(t, func(w http.ResponseWriter, r *http.Request, n int32) {
		if r.Header.Get("Content-Type") != "application/json" {
			t.Error("no JSON content type")
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		writeJSON(w, 200, `{"name":"www","type":"A","ttl":300,"rdata":["203.0.113.1"]}`)
	})
	rec, err := c.DNSRecord.UpsertDNSRecord(context.Background(), "zone-1", &UpsertDNSRecordBody{
		Record: &DNSRecord{Name: "www", Type: "A", TTL: 300, Rdata: []string{"203.0.113.1"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if rec.Name != "www" || rec.TTL != 300 {
		t.Errorf("record %+v", rec)
	}
	r := body["record"].(map[string]any)
	if r["name"] != "www" || r["ttl"].(float64) != 300 {
		t.Errorf("body %v", body)
	}
	if _, ok := r["system"]; ok {
		t.Error("zero/unset fields must be left out")
	}
}

func TestBoolPointerSendsFalse(t *testing.T) {
	var body map[string]any
	c, _ := fakeAPI(t, func(w http.ResponseWriter, r *http.Request, n int32) {
		_ = json.NewDecoder(r.Body).Decode(&body)
		writeJSON(w, 200, `{"success":true}`)
	})
	if _, err := c.Snapshot.RestoreSnapshot(context.Background(), "vm", "snap", &RestoreSnapshotBody{StopVM: Ptr(false)}); err != nil {
		t.Fatal(err)
	}
	if v, ok := body["stopVm"]; !ok || v != false {
		t.Errorf("explicit false not sent: %v", body)
	}
}

func TestIdempotencyKey(t *testing.T) {
	var keys []string
	c, _ := fakeAPI(t, func(w http.ResponseWriter, r *http.Request, n int32) {
		var b CreateVirtualMachineRequest
		_ = json.NewDecoder(r.Body).Decode(&b)
		keys = append(keys, b.IdempotencyKey)
		writeJSON(w, 200, `{"success":true,"virtualMachine":{"id":"vm-1"}}`)
	})
	req := &CreateVirtualMachineRequest{Hostname: "web-1"}
	if _, err := c.VirtualMachine.CreateVirtualMachine(context.Background(), req); err != nil {
		t.Fatal(err)
	}
	uuid := regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)
	if !uuid.MatchString(keys[0]) || req.IdempotencyKey != keys[0] {
		t.Fatalf("generated key %q, body key %q", keys[0], req.IdempotencyKey)
	}
	// A retry of the same request reuses the key; a set key is kept.
	_, _ = c.VirtualMachine.CreateVirtualMachine(context.Background(), req)
	_, _ = c.VirtualMachine.CreateVirtualMachine(context.Background(), &CreateVirtualMachineRequest{IdempotencyKey: "mine"})
	if keys[1] != keys[0] || keys[2] != "mine" {
		t.Errorf("keys %v", keys)
	}
}

func TestRetries(t *testing.T) {
	ctx := context.Background()
	t.Run("429 is retried", func(t *testing.T) {
		c, n := fakeAPI(t, func(w http.ResponseWriter, r *http.Request, n int32) {
			if n < 3 {
				writeJSON(w, 429, `{"code":8,"message":"slow down"}`)
				return
			}
			writeJSON(w, 200, `{}`)
		})
		if _, err := c.DNSZone.CreateDNSZone(ctx, &CreateDNSZoneRequest{Name: "x.test"}); err != nil {
			t.Fatal(err)
		}
		if n.Load() != 3 {
			t.Errorf("%d requests, want 3", n.Load())
		}
	})
	t.Run("exhausted quota is not retried", func(t *testing.T) {
		c, n := fakeAPI(t, func(w http.ResponseWriter, r *http.Request, n int32) {
			writeJSON(w, 429, `{"code":8,"message":"limit","details":[{"@type":"type.googleapis.com/google.rpc.ErrorInfo","reason":"VM_LIMIT_REACHED","metadata":{"limit":"5"}}]}`)
		})
		_, err := c.VirtualMachine.CreateVirtualMachine(ctx, &CreateVirtualMachineRequest{})
		if n.Load() != 1 || !IsQuotaExceeded(err) || IsRateLimited(err) {
			t.Fatalf("%d requests, err %v", n.Load(), err)
		}
		var e *Error
		if !errors.As(err, &e) || e.Reason() != "VM_LIMIT_REACHED" || e.Details[0].Metadata["limit"] != "5" {
			t.Errorf("details %+v", e)
		}
	})
	t.Run("503 on a read is retried", func(t *testing.T) {
		c, n := fakeAPI(t, func(w http.ResponseWriter, r *http.Request, n int32) {
			if n == 1 {
				writeJSON(w, 503, `{"code":14,"message":"unavailable"}`)
				return
			}
			writeJSON(w, 200, `{"distros":[{"distro":"DISTRO_UBUNTU_26_04"}]}`)
		})
		out, err := c.Metadata.ListDistros(ctx)
		if err != nil || n.Load() != 2 || out.Distros[0].Distro != DistroUbuntu2604 {
			t.Fatalf("%d requests, %v %+v", n.Load(), err, out)
		}
	})
	t.Run("502 on a plain create is not retried", func(t *testing.T) {
		c, n := fakeAPI(t, func(w http.ResponseWriter, r *http.Request, _ int32) { writeJSON(w, 502, `bad gateway`) })
		_, err := c.SSHKey.CreateSSHKey(ctx, &CreateSSHKeyRequest{Name: "k"})
		if n.Load() != 1 || err == nil || ErrorCode(err) != CodeUnknown {
			t.Fatalf("%d requests, %v", n.Load(), err)
		}
		if !strings.Contains(err.Error(), "bad gateway") || !strings.Contains(err.Error(), "POST /v1/ssh-keys") {
			t.Errorf("message %q", err)
		}
	})
	t.Run("502 on a create with an idempotency key is retried", func(t *testing.T) {
		c, n := fakeAPI(t, func(w http.ResponseWriter, r *http.Request, n int32) {
			if n == 1 {
				writeJSON(w, 502, ``)
				return
			}
			writeJSON(w, 200, `{"success":true}`)
		})
		if _, err := c.VirtualMachine.CreateVirtualMachine(ctx, &CreateVirtualMachineRequest{}); err != nil || n.Load() != 2 {
			t.Fatalf("%d requests, %v", n.Load(), err)
		}
	})
	t.Run("gives up after the retry limit", func(t *testing.T) {
		c, n := fakeAPI(t, func(w http.ResponseWriter, r *http.Request, n int32) { writeJSON(w, 503, `{}`) })
		_, err := c.Organization.GetOrganization(ctx)
		if n.Load() != 4 || ErrorCode(err) != CodeUnavailable {
			t.Fatalf("%d requests, %v", n.Load(), err)
		}
	})
	t.Run("4xx is not retried", func(t *testing.T) {
		c, n := fakeAPI(t, func(w http.ResponseWriter, r *http.Request, n int32) {
			writeJSON(w, 404, `{"code":5,"message":"virtual machine not found"}`)
		})
		_, err := c.VirtualMachine.GetVirtualMachine(ctx, "nope")
		if n.Load() != 1 || !IsNotFound(err) {
			t.Fatalf("%d requests, %v", n.Load(), err)
		}
	})
	t.Run("Retry-After is honored up to the maximum backoff", func(t *testing.T) {
		c, _ := fakeAPI(t, func(w http.ResponseWriter, r *http.Request, n int32) {
			if n == 1 {
				w.Header().Set("Retry-After", "1")
				writeJSON(w, 429, `{}`)
				return
			}
			writeJSON(w, 200, `{}`)
		})
		c.maxBackoff = 50 * time.Millisecond
		start := time.Now()
		if _, err := c.Organization.GetOrganization(ctx); err != nil {
			t.Fatal(err)
		}
		if d := time.Since(start); d < 40*time.Millisecond || d > 900*time.Millisecond {
			t.Errorf("waited %v, want about the 50ms cap", d)
		}
	})
	t.Run("context cancel stops retrying", func(t *testing.T) {
		c, _ := fakeAPI(t, func(w http.ResponseWriter, r *http.Request, n int32) { writeJSON(w, 503, `{}`) })
		c.minBackoff, c.maxBackoff = time.Second, time.Second
		ctx, cancel := context.WithTimeout(ctx, 30*time.Millisecond)
		defer cancel()
		start := time.Now()
		_, err := c.Organization.GetOrganization(ctx)
		if err == nil || time.Since(start) > 500*time.Millisecond {
			t.Fatalf("err %v after %v", err, time.Since(start))
		}
	})
	t.Run("connection errors on reads are retried", func(t *testing.T) {
		c, _ := NewClient("t", WithBaseURL("http://127.0.0.1:1"), WithRetries(2, time.Millisecond, time.Millisecond))
		_, err := c.Organization.GetOrganization(ctx)
		if err == nil {
			t.Fatal("expected a connection error")
		}
		var e *Error
		if errors.As(err, &e) {
			t.Error("connection error reported as an API error")
		}
	})
}

func TestErrorHelpers(t *testing.T) {
	cases := []struct {
		status int
		body   string
		check  func(error) bool
	}{
		{401, `{"code":16,"message":"bad token"}`, IsUnauthenticated},
		{403, `{"code":7,"message":"no"}`, IsPermissionDenied},
		{400, `{"code":3,"message":"hostname is required"}`, IsInvalidArgument},
		{409, `{"code":6,"message":"exists"}`, IsAlreadyExists},
		{404, `not json`, IsNotFound},
		{400, `{"code":9,"message":"operation in progress"}`, IsBusy},
		{429, `{"code":8,"message":"slow"}`, IsRateLimited},
	}
	for _, tc := range cases {
		c, _ := fakeAPI(t, func(w http.ResponseWriter, r *http.Request, n int32) { writeJSON(w, tc.status, tc.body) })
		c.maxRetries = 0
		_, err := c.Organization.GetOrganization(context.Background())
		if !tc.check(err) {
			t.Errorf("%d %s: helper false for %v (code %d)", tc.status, tc.body, err, ErrorCode(err))
		}
	}
	if ErrorCode(nil) != CodeOK || ErrorCode(errors.New("x")) != CodeUnknown || IsNotFound(nil) {
		t.Error("helpers on nil and foreign errors")
	}
}

func TestIterators(t *testing.T) {
	const total = 230
	var pages []string
	c, _ := fakeAPI(t, func(w http.ResponseWriter, r *http.Request, n int32) {
		q := r.URL.Query()
		pages = append(pages, q.Get("page")+"/"+q.Get("pageSize"))
		page, size := atoi(q.Get("page")), atoi(q.Get("pageSize"))
		var keys []string
		for i := page * size; i < min(total, (page+1)*size); i++ {
			keys = append(keys, `{"id":"k`+itoa(i)+`"}`)
		}
		writeJSON(w, 200, `{"sshKeys":[`+strings.Join(keys, ",")+`],"totalCount":`+itoa(total)+`}`)
	})
	ctx := context.Background()
	all, err := Collect(c.SSHKey.ListSSHKeysAll(ctx, nil))
	if err != nil || len(all) != total || all[229].ID != "k229" {
		t.Fatalf("%d keys, %v", len(all), err)
	}
	if strings.Join(pages, " ") != "/100 1/100 2/100" {
		t.Errorf("pages %v", pages)
	}
	// Early break makes no further requests.
	pages = nil
	n := 0
	for k, err := range c.SSHKey.ListSSHKeysAll(ctx, &ListSSHKeysParams{PageSize: 10, Page: 3}) {
		if err != nil {
			t.Fatal(err)
		}
		n++
		if k.ID == "k34" {
			break
		}
	}
	if n != 5 || len(pages) != 1 {
		t.Errorf("read %d items with %d requests", n, len(pages))
	}
}

func TestIteratorError(t *testing.T) {
	c, _ := fakeAPI(t, func(w http.ResponseWriter, r *http.Request, n int32) {
		if r.URL.Query().Get("page") == "1" {
			writeJSON(w, 403, `{"code":7,"message":"denied"}`)
			return
		}
		writeJSON(w, 200, `{"zones":[`+strings.Repeat(`{"id":"z"},`, 99)+`{"id":"z"}],"totalCount":150}`)
	})
	got, err := Collect(c.DNSZone.ListDNSZonesAll(context.Background(), nil))
	if len(got) != 100 || !IsPermissionDenied(err) {
		t.Fatalf("%d zones, %v", len(got), err)
	}
}

func TestWaitForVirtualMachine(t *testing.T) {
	statuses := []string{"OPERATION_STATUS_CREATED", "OPERATION_STATUS_CREATED", "OPERATION_STATUS_STARTED"}
	c, n := fakeAPI(t, func(w http.ResponseWriter, r *http.Request, n int32) {
		i := min(int(n)-1, len(statuses)-1)
		writeJSON(w, 200, `{"id":"vm","operationStatus":"`+statuses[i]+`"}`)
	})
	vm, err := c.WaitForVirtualMachineStatus(context.Background(), "vm", &WaitOptions{Interval: time.Millisecond}, OperationStatusStarted)
	if err != nil || vm.OperationStatus != OperationStatusStarted || n.Load() != 3 {
		t.Fatalf("%v %v after %d polls", vm, err, n.Load())
	}
	// Timeout reports the last status.
	_, err = c.WaitForVirtualMachineStatus(context.Background(), "vm", &WaitOptions{Interval: time.Millisecond, Timeout: 20 * time.Millisecond}, OperationStatusStopped)
	if err == nil || !strings.Contains(err.Error(), "OPERATION_STATUS_STARTED") {
		t.Errorf("timeout error %v", err)
	}
	// Deleted: a 404 counts as done.
	d, _ := fakeAPI(t, func(w http.ResponseWriter, r *http.Request, n int32) { writeJSON(w, 404, `{"code":5}`) })
	if err := d.WaitForVirtualMachineDeleted(context.Background(), "vm", &WaitOptions{Interval: time.Millisecond}); err != nil {
		t.Errorf("deleted wait: %v", err)
	}
}

func TestDo(t *testing.T) {
	c, _ := fakeAPI(t, func(w http.ResponseWriter, r *http.Request, n int32) {
		if r.URL.Path != "/v1/custom" || r.URL.Query().Get("a") != "b" {
			t.Errorf("request %s", r.URL)
		}
		writeJSON(w, 200, `{"ok":true}`)
	})
	var out struct{ OK bool }
	if err := c.Do(context.Background(), http.MethodPost, "/v1/custom", map[string][]string{"a": {"b"}}, map[string]int{"x": 1}, &out); err != nil || !out.OK {
		t.Fatalf("%v %v", out, err)
	}
}

func TestInt64s(t *testing.T) {
	var v Int64s
	if err := json.Unmarshal([]byte(`["1", 2, "9007199254740993"]`), &v); err != nil || v[2] != 9007199254740993 {
		t.Fatalf("%v %v", v, err)
	}
	b, _ := json.Marshal(Int64s{5, 9007199254740993})
	if string(b) != `["5","9007199254740993"]` {
		t.Errorf("%s", b)
	}
}

func atoi(s string) int {
	n := 0
	for _, r := range s {
		n = n*10 + int(r-'0')
	}
	return n
}

func itoa(i int) string {
	return strings.TrimSpace(strings.Replace(string(rune('0'+0)), "0", "", 1)) + intString(i)
}

func intString(i int) string {
	if i == 0 {
		return "0"
	}
	var b []byte
	for i > 0 {
		b = append([]byte{byte('0' + i%10)}, b...)
		i /= 10
	}
	return string(b)
}
