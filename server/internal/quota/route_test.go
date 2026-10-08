package quota

import "testing"

func TestRouteAndPauseWithoutARouter(t *testing.T) {
	l := testLocal(t, nil)
	h := newTestService(l, NewStore("")).Handler()
	if code, _ := call(t, h, "GET", "/api/route", "", nil); code != 400 {
		t.Fatalf("no model: %d", code)
	}
	code, out := call(t, h, "GET", "/api/route?model=glm-5.3", "", nil)
	if code != 200 || len(out["use"].([]any)) != 0 || len(out["notUsed"].([]any)) != 0 {
		t.Fatalf("no router: %d %v", code, out)
	}
	if code, _ := call(t, h, "POST", "/api/pause", `{"id":"nope","paused":true}`, nil); code != 400 {
		t.Fatalf("pause without a router: %d", code)
	}
}
