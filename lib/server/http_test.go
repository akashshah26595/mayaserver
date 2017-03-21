package server

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"io/ioutil"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"testing"
	"time"

	"github.com/openebs/mayaserver/lib/config"
	"github.com/openebs/mayaserver/structs"
	"github.com/ugorji/go/codec"
)

type TestServer struct {
	T      testing.TB
	Dir    string
	Maya   *MayaApiServer
	Server *HTTPServer
}

func (s *TestServer) Cleanup() {
	s.Server.Shutdown()
	s.Maya.Shutdown()
	os.RemoveAll(s.Dir)
}

// makeHTTPTestServer returns a test server with full logging.
func makeHTTPTestServer(t testing.TB, fnmc func(mc *config.MayaConfig)) *TestServer {
	return makeHTTPTestServerWithWriter(t, nil, fnmc)
}

// makeHTTPTestServerNoLogs returns a test server which only prints maya logs and
// no http server logs
func makeHTTPTestServerNoLogs(t testing.TB, fnmc func(mc *config.MayaConfig)) *TestServer {
	return makeHTTPTestServerWithWriter(t, ioutil.Discard, fnmc)
}

// makeHTTPTestServerWithWriter returns a test server whose logs will be written to
// the passed writer. If the writer is nil, the logs are written to stderr.
func makeHTTPTestServerWithWriter(t testing.TB, w io.Writer, fnmc func(mc *config.MayaConfig)) *TestServer {
	dir, maya := makeMayaServer(t, fnmc)
	if w == nil {
		w = maya.logOutput
	}
	srv, err := NewHTTPServer(maya, maya.config, w)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	s := &TestServer{
		T:      t,
		Dir:    dir,
		Maya:   maya,
		Server: srv,
	}
	return s
}

func BenchmarkHTTPRequests(b *testing.B) {
	s := makeHTTPTestServerNoLogs(b, func(mc *config.MayaConfig) {

	})
	defer s.Cleanup()

	handler := func(resp http.ResponseWriter, req *http.Request) (interface{}, error) {
		// TODO we are returing a num;
		// instead return some big payload i.e. big array of any structure
		return 1000, nil
	}
	b.ResetTimer()

	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			resp := httptest.NewRecorder()
			req, _ := http.NewRequest("GET", "/v1/kv/key", nil)
			s.Server.wrap(handler)(resp, req)
		}
	})
}

func TestSetIndex(t *testing.T) {
	resp := httptest.NewRecorder()
	setIndex(resp, 1000)
	header := resp.Header().Get("X-Maya-Index")
	if header != "1000" {
		t.Fatalf("Bad: %v", header)
	}
	setIndex(resp, 2000)
	if v := resp.Header()["X-Maya-Index"]; len(v) != 1 {
		t.Fatalf("bad: %#v", v)
	}
}

func TestSetLastContact(t *testing.T) {
	resp := httptest.NewRecorder()
	setLastContact(resp, 123456*time.Microsecond)
	header := resp.Header().Get("X-Maya-LastContact")
	if header != "123" {
		t.Fatalf("Bad: %v", header)
	}
}

func TestSetMeta(t *testing.T) {
	qm := structs.QueryMeta{
		Index:       1000,
		LastContact: 123456 * time.Microsecond,
	}
	resp := httptest.NewRecorder()
	setMeta(resp, &qm)
	header := resp.Header().Get("X-Maya-Index")
	if header != "1000" {
		t.Fatalf("Bad: %v", header)
	}
	header = resp.Header().Get("X-Maya-LastContact")
	if header != "123" {
		t.Fatalf("Bad: %v", header)
	}
}

func TestSetHeaders(t *testing.T) {
	s := makeHTTPTestServer(t, nil)
	s.Maya.config.HTTPAPIResponseHeaders = map[string]string{"foo": "bar"}
	defer s.Cleanup()

	resp := httptest.NewRecorder()
	handler := func(resp http.ResponseWriter, req *http.Request) (interface{}, error) {
		return "noop", nil
	}

	req, _ := http.NewRequest("GET", "/v1/kv/key", nil)
	s.Server.wrap(handler)(resp, req)
	header := resp.Header().Get("foo")

	if header != "bar" {
		t.Fatalf("expected header: %v, actual: %v", "bar", header)
	}

}

func TestContentTypeIsJSON(t *testing.T) {
	s := makeHTTPTestServer(t, nil)
	defer s.Cleanup()

	resp := httptest.NewRecorder()

	handler := func(resp http.ResponseWriter, req *http.Request) (interface{}, error) {
		return "noop", nil
	}

	req, _ := http.NewRequest("GET", "/v1/kv/key", nil)
	s.Server.wrap(handler)(resp, req)

	contentType := resp.Header().Get("Content-Type")

	if contentType != "application/json" {
		t.Fatalf("Content-Type header was not 'application/json'")
	}
}

func TestPrettyPrint(t *testing.T) {
	testPrettyPrint("pretty=1", true, t)
}

func TestPrettyPrintOff(t *testing.T) {
	testPrettyPrint("pretty=0", false, t)
}

func TestPrettyPrintBare(t *testing.T) {
	testPrettyPrint("pretty", true, t)
}

func testPrettyPrint(pretty string, prettyFmt bool, t *testing.T) {
	s := makeHTTPTestServer(t, nil)
	defer s.Cleanup()

	r := struct {
		Name string
		Role string
		Org  string
	}{
		"das",
		"hacker",
		"openebs",
	}

	resp := httptest.NewRecorder()
	handler := func(resp http.ResponseWriter, req *http.Request) (interface{}, error) {
		return r, nil
	}

	urlStr := "/v1/kv/key?" + pretty
	req, _ := http.NewRequest("GET", urlStr, nil)
	s.Server.wrap(handler)(resp, req)

	var expected bytes.Buffer
	if prettyFmt {
		enc := codec.NewEncoder(&expected, jsonHandlePretty)
		err := enc.Encode(r)
		if err == nil {
			expected.Write([]byte("\n"))
		} else {
			t.Fatalf("err while pretty encoding: %v", err)
		}
	} else {
		enc := codec.NewEncoder(&expected, jsonHandle)
		err := enc.Encode(r)

		if err != nil {
			t.Fatalf("err while encoding: %v", err)
		}
	}

	actual, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("err: %s", err)
	}

	if !bytes.Equal(expected.Bytes(), actual) {
		t.Fatalf("bad:\nexpected:\t%q\n\nactual:\t\t%q", string(expected.Bytes()), string(actual))
	}
}

func TestParseWait(t *testing.T) {

	var qo structs.QueryOptions

	s := makeHTTPTestServer(t, nil)
	defer s.Cleanup()

	resp := httptest.NewRecorder()
	handler := func(resp http.ResponseWriter, req *http.Request) (interface{}, error) {
		return "noop", nil
	}

	req, err := http.NewRequest("GET",
		"/v1/kv/key?wait=60s&index=1000", nil)

	if err != nil {
		t.Fatalf("err: %v", err)
	}

	s.Server.wrap(handler)(resp, req)

	if d := parseWait(resp, req, &qo); d {
		t.Fatalf("unexpected done")
	}

	if qo.MinQueryIndex != 1000 {
		t.Fatalf("Bad query index: %v", qo)
	}

	if qo.MaxQueryTime != 60*time.Second {
		t.Fatalf("Bad query time: %v", qo)
	}
}

func TestParseWait_InvalidTime(t *testing.T) {

	var qo structs.QueryOptions

	s := makeHTTPTestServer(t, nil)
	defer s.Cleanup()

	resp := httptest.NewRecorder()

	req, err := http.NewRequest("GET",
		"/v1/kv/key?wait=60foo&index=1000", nil)

	if err != nil {
		t.Fatalf("err: %v", err)
	}

	if d := parseWait(resp, req, &qo); !d {
		t.Fatalf("expected done")
	}

	if resp.Code != 400 {
		t.Fatalf("bad http code: expected code: 400, got code: %v", resp.Code)
	}
}

func TestParseWait_InvalidIndex(t *testing.T) {

	var qo structs.QueryOptions

	s := makeHTTPTestServer(t, nil)
	defer s.Cleanup()

	resp := httptest.NewRecorder()
	//handler := func(resp http.ResponseWriter, req *http.Request) (interface{}, error) {
	//	return "noop", nil
	//}

	req, err := http.NewRequest("GET",
		"/v1/kv/key?wait=60s&index=foo", nil)

	if err != nil {
		t.Fatalf("err: %v", err)
	}

	//s.Server.wrap(handler)(resp, req)

	if d := parseWait(resp, req, &qo); !d {
		t.Fatalf("expected done")
	}

	if resp.Code != 400 {
		t.Fatalf("bad http code: expected: 400, got: %v", resp.Code)
	}
}

func TestParseConsistency(t *testing.T) {
	var qo structs.QueryOptions

	req, err := http.NewRequest("GET",
		"/v1/kv/key?stale", nil)

	if err != nil {
		t.Fatalf("err: %v", err)
	}

	parseConsistency(req, &qo)

	if !qo.AllowStale {
		t.Fatalf("Bad: %v", qo)
	}

	// reset the query options
	qo = structs.QueryOptions{}
	req, err = http.NewRequest("GET",
		"/v1/kv/key?consistent", nil)

	if err != nil {
		t.Fatalf("err: %v", err)
	}

	parseConsistency(req, &qo)

	if qo.AllowStale {
		t.Fatalf("Bad: %v", qo)
	}
}

func TestParseRegion(t *testing.T) {

	var region string

	s := makeHTTPTestServer(t, nil)
	defer s.Cleanup()

	req, err := http.NewRequest("GET",
		"/v1/kv/key?region=foo", nil)

	if err != nil {
		t.Fatalf("err: %v", err)
	}

	s.Server.parseRegion(req, &region)

	if region != "foo" {
		t.Fatalf("bad %s", region)
	}

	// reset the region
	region = ""
	req, err = http.NewRequest("GET", "/v1/kv/key", nil)
	if err != nil {
		t.Fatalf("err: %v", err)
	}

	s.Server.parseRegion(req, &region)
	if region != "global" {
		t.Fatalf("bad %s", region)
	}
}

// assertIndex tests that X-Maya-Index is set and non-zero
func assertIndex(t *testing.T, resp *httptest.ResponseRecorder) {
	header := resp.Header().Get("X-Maya-Index")
	if header == "" || header == "0" {
		t.Fatalf("Bad: %v", header)
	}
}

// checkIndex is like assertIndex but returns an error
func checkIndex(resp *httptest.ResponseRecorder) error {
	header := resp.Header().Get("X-Maya-Index")
	if header == "" || header == "0" {
		return fmt.Errorf("Bad: %v", header)
	}
	return nil
}

// getIndex parses X-Maya-Index
func getIndex(t *testing.T, resp *httptest.ResponseRecorder) uint64 {
	header := resp.Header().Get("X-Maya-Index")
	if header == "" {
		t.Fatalf("Bad: %v", header)
	}
	val, err := strconv.Atoi(header)
	if err != nil {
		t.Fatalf("Bad: %v", header)
	}
	return uint64(val)
}

func httpTest(t testing.TB, fnmc func(mc *config.MayaConfig), f func(srv *TestServer)) {
	s := makeHTTPTestServer(t, fnmc)
	defer s.Cleanup()
	f(s)
}

func encodeReq(obj interface{}) io.ReadCloser {
	buf := bytes.NewBuffer(nil)
	enc := json.NewEncoder(buf)
	enc.Encode(obj)
	return ioutil.NopCloser(buf)
}
