package server

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestMosDNSLearningRequiresPositiveRoutingEvidence(t *testing.T) {
	entry := func(domain, qt, code, tag, ip string) map[string]any {
		e := map[string]any{"query_name": domain, "query_type": qt, "response_code": code, "domain_set": tag, "answers": []any{}}
		if ip != "" {
			e["answers"] = []any{map[string]any{"type": qt, "data": ip}}
		}
		return e
	}
	entries := []map[string]any{
		entry("api.x.com", "A", "NOERROR", "订阅代理", "28.0.1.5"),
		entry("api.x.com", "AAAA", "NOERROR", "订阅代理", ""),
		entry("error.test", "A", "SERVFAIL", "白名单", "223.5.5.5"),
		entry("empty.test", "A", "NOERROR", "白名单", ""),
		entry("unknown.test", "A", "NOERROR", "未命中", "223.5.5.5"),
		entry("stale.test", "A", "NOERROR", "记忆直连", "223.5.5.5"),
		entry("proxy-real.test", "A", "NOERROR", "订阅代理", "162.159.140.229"),
		entry("direct.test", "A", "NOERROR", "订阅直连", "223.5.5.5"),
		entry("conflict.test", "A", "NOERROR", "订阅直连", "223.5.5.5"),
		entry("conflict.test", "AAAA", "NOERROR", "订阅代理", "f2b0::2"),
		entry("same-record.test", "A", "NOERROR", "订阅直连|订阅代理", "223.5.5.5"),
		entry("invalid.test", "A", "NOERROR", "白名单", "not-an-ip"),
		entry("loop.test", "A", "NOERROR", "白名单", "127.0.0.1"),
	}
	entries = append(entries, normalizeMosDNSQueryMap(map[string]any{
		"query_name": "missing-status.test", "query_type": "A", "domain_set": "白名单", "answers": []any{"A: 223.5.5.5"},
	}, 0, ""))
	fake, real := mosDNSLearnedDomains(entries, SetupConfig{})
	if !reflect.DeepEqual(fake, []string{"api.x.com", "proxy-real.test"}) || !reflect.DeepEqual(real, []string{"direct.test"}) {
		t.Fatalf("fake=%v real=%v", fake, real)
	}
}

func TestMosDNSFakeIPParsingAndCustomRanges(t *testing.T) {
	for _, answer := range []any{"A: 128.0.0.1", map[string]any{"type": "A", "ttl": 28, "data": "1.1.1.1"}, map[string]any{"type": "CNAME", "data": "28.example"}} {
		if entryHasFakeIP(map[string]any{"answers": []any{answer}}) {
			t.Fatalf("false FakeIP: %v", answer)
		}
	}
	e := map[string]any{"query_name": "custom.test", "query_type": "A", "response_code": "NOERROR", "answers": []any{"A: 100.64.1.1"}}
	f, r := mosDNSLearnedDomains([]map[string]any{e}, SetupConfig{FakeIPRangeV4: "100.64.0.0/10", FakeIPRangeV6: "fc00::/7"})
	if len(f) != 1 || len(r) != 0 {
		t.Fatalf("custom ranges: %v %v", f, r)
	}
	if got := mosDNSExactRules([]string{"api.x.com"}); !reflect.DeepEqual(got, []string{"full:api.x.com"}) {
		t.Fatal(got)
	}
}

func TestMosDNSLearningMigrationPreservesUserRules(t *testing.T) {
	a := newTestApp(t)
	if err := os.Remove(filepath.Join(a.DataDir, "data/mosdns-learning-v2")); err != nil {
		t.Fatal(err)
	}
	for _, rel := range []string{"configs/mosdns/gen/realiprule.txt", "configs/mosdns/gen/fakeiprule.txt", "configs/mosdns/gen/nov6rule.txt", "configs/mosdns/cache/cache_cn.dump"} {
		if err := a.writeTextFile(rel, "stale"); err != nil {
			t.Fatal(err)
		}
	}
	const user = "configs/mosdns/rule/greylist.txt"
	if err := a.writeTextFile(user, "domain:x.com\n"); err != nil {
		t.Fatal(err)
	}
	if err := a.ensureMosDNSLearningSafety(); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(filepath.Join(a.DataDir, user)); string(b) != "domain:x.com\n" {
		t.Fatal("user rule changed")
	}
	for _, name := range []string{"realiprule.txt", "fakeiprule.txt", "nov6rule.txt"} {
		if b, err := os.ReadFile(filepath.Join(a.DataDir, "configs/mosdns/gen", name)); err != nil || len(b) != 0 {
			t.Fatalf("%s: %q %v", name, b, err)
		}
	}
	if _, err := os.Stat(filepath.Join(a.DataDir, "configs/mosdns/cache/cache_cn.dump")); !os.IsNotExist(err) {
		t.Fatal("stale cache retained")
	}
	if err := a.writeTextFile("configs/mosdns/gen/realiprule.txt", "full:new.test\n"); err != nil {
		t.Fatal(err)
	}
	if err := a.ensureMosDNSLearningSafety(); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(filepath.Join(a.DataDir, "configs/mosdns/gen/realiprule.txt")); len(b) == 0 {
		t.Fatal("migration ran twice")
	}
}
