package server

import (
	"bytes"
	"context"
	"encoding/binary"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"golang.org/x/net/dns/dnsmessage"
	"gopkg.in/yaml.v3"
)

// Run with MSF_TEST_MOSDNS pointing at the yyysuo/mosdns executable. All
// upstreams are deterministic in-process plugins; no public DNS is contacted.
func TestMosDNSLearningRuntime(t *testing.T) {
	binaryPath := os.Getenv("MSF_TEST_MOSDNS")
	if binaryPath == "" {
		t.Skip("set MSF_TEST_MOSDNS to run real-core routing regression")
	}
	for _, mode := range []string{"A", "B"} {
		t.Run(mode, func(t *testing.T) {
			dir := t.TempDir()
			type plugin = map[string]any
			var plugins []plugin
			add := func(tag, typ string, args any) {
				plugins = append(plugins, plugin{"tag": tag, "type": typ, "args": args})
			}
			rule := func(match, action string) map[string]any {
				r := map[string]any{"exec": action}
				if match != "" {
					r["matches"] = match
				}
				return r
			}
			for _, s := range []struct{ tag, value string }{{"switch3", mode}, {"switch9", "A"}, {"switch4", "B"}} {
				p := filepath.Join(dir, s.tag)
				if err := os.WriteFile(p, []byte(s.value), 0644); err != nil {
					t.Fatal(err)
				}
				add(s.tag, s.tag, map[string]any{"initial_value": p})
			}
			add("direct_ip", "ip_set", map[string]any{"ips": []string{}})
			add("geoip_cn", "ip_set", map[string]any{"ips": []string{"223.5.5.0/24", "2400:3200::/32"}})
			add("unified_matcher4", "sequence", []any{})
			add("unified_matcher3", "sequence", []any{})
			add("old_direct", "domain_set_light", map[string]any{"exps": []string{"full:proxy.test", "full:conflict.test"}})
			add("old_proxy", "domain_set_light", map[string]any{"exps": []string{"full:domestic.test", "full:conflict.test"}})
			add("online_proxy", "domain_set_light", map[string]any{"exps": []string{"domain:proxy.test"}})
			add("online_direct", "domain_set_light", map[string]any{"exps": []string{"domain:domestic.test"}})
			add("unified_matcher5", "domain_mapper", map[string]any{"default_mark": 17, "rules": []any{
				map[string]any{"tag": "old_direct", "mark": 11},
				map[string]any{"tag": "old_proxy", "mark": 12},
				map[string]any{"tag": "online_proxy", "mark": 14},
				map[string]any{"tag": "online_direct", "mark": 16},
			}})
			for _, tag := range []string{"cache_cn", "cache_cnmihomo", "my_nov4list", "my_nov6list"} {
				add(tag, "sequence", []any{})
			}
			add("sequence_local", "sequence", []any{rule("", "black_hole 223.5.5.5 2400:3200::1")})
			add("domestic", "sequence", []any{rule("", "$sequence_local")})
			add("cnfake", "sequence", []any{rule("", "black_hole 28.0.0.6 f2b0::6")})
			add("nocnfake", "sequence", []any{rule("", "black_hole 28.0.0.5 f2b0::5")})
			for _, tag := range []string{"sequence_google", "sequence_google_node"} {
				add(tag, "sequence", []any{rule("qname full:failed.test", "reject 2"), rule("qname full:empty.test", "reject 0"), rule("qname full:learn-direct.test", "black_hole 223.5.5.5 2400:3200::1"), rule("has_resp", "return"), rule("", "black_hole 198.51.100.10 2001:db8::10")})
			}
			for _, tag := range []string{"my_realiplist", "my_fakeiplist"} {
				add(tag, "domain_output", map[string]any{"file_stat": filepath.Join(dir, tag+".txt"), "file_rule": filepath.Join(dir, tag+".rules"), "max_entries": 1000, "dump_interval": 3600})
			}
			// Load production sequences verbatim, substituting only deterministic upstreams.
			selected := map[string]bool{"sequence_local_exit": true, "sequence_local_fake": true, "sequence_local_divert": true, "sequence_local_fake_exit": true, "sequence_fakeip": true, "sequence_fakeip_addlist": true}
			for _, name := range []string{"forward_1.yaml", "not_in_list_ipmatch.yaml", "not_in_list_leak_v4.yaml", "not_in_list_leak_v6.yaml", "not_in_list_noleak_v4.yaml", "not_in_list_noleak_v6.yaml", "process_v4.yaml", "process_v6.yaml"} {
				var doc struct {
					Plugins []plugin `yaml:"plugins"`
				}
				if err := yaml.Unmarshal(mustMosDNSRuntimeTemplate(t, "mosdns/sub_config/"+name), &doc); err != nil {
					t.Fatal(err)
				}
				for _, p := range doc.Plugins {
					if name != "forward_1.yaml" || selected[fmt.Sprint(p["tag"])] {
						plugins = append(plugins, p)
					}
				}
			}
			add("main", "sequence", []any{rule("qtype 1", "$sequence_ipv4"), rule("qtype 28", "$sequence_ipv6")})
			conn, err := net.ListenPacket("udp", "127.0.0.1:0")
			if err != nil {
				t.Fatal(err)
			}
			addr := conn.LocalAddr().String()
			conn.Close()
			add("test_dns", "udp_server", map[string]any{"entry": "main", "listen": addr})
			conf, _ := yaml.Marshal(map[string]any{"log": map[string]any{"level": "error"}, "plugins": plugins})
			path := filepath.Join(dir, "config.yaml")
			if err := os.WriteFile(path, conf, 0644); err != nil {
				t.Fatal(err)
			}
			var output bytes.Buffer
			cmd := exec.Command(binaryPath, "start", "-c", path)
			cmd.Dir = dir
			cmd.Stdout = &output
			cmd.Stderr = &output
			if err := cmd.Start(); err != nil {
				t.Fatal(err)
			}
			stopped := false
			stop := func() {
				if !stopped {
					cmd.Process.Signal(os.Interrupt)
					cmd.Wait()
					stopped = true
				}
			}
			defer stop()
			ready := false
			for i := 0; i < 50; i++ {
				if _, err := learningDNSQuery(addr, "proxy.test", dnsmessage.TypeA); err == nil {
					ready = true
					break
				}
				time.Sleep(50 * time.Millisecond)
			}
			if !ready {
				stop()
				t.Fatalf("core failed to start: %s", output.String())
			}
			for _, qt := range []dnsmessage.Type{dnsmessage.TypeA, dnsmessage.TypeAAAA} {
				for _, name := range []string{"proxy.test", "domestic.test", "conflict.test", "unknown.test", "learn-direct.test", "failed.test", "empty.test"} {
					msg, err := learningDNSQuery(addr, name, qt)
					if err != nil {
						t.Fatal(err)
					}
					want := "28.0.0.5"
					if qt == dnsmessage.TypeAAAA {
						want = "f2b0::5"
					}
					if name == "domestic.test" || (mode == "A" && name != "proxy.test") || name == "learn-direct.test" {
						want = "223.5.5.5"
						if qt == dnsmessage.TypeAAAA {
							want = "2400:3200::1"
						}
					}
					if mode == "B" && (name == "failed.test" || name == "empty.test") {
						if len(msg.Answers) != 0 {
							t.Fatalf("%s should have no answer: %v", name, msg)
						}
						continue
					}
					found := false
					for _, r := range msg.Answers {
						switch body := r.Body.(type) {
						case *dnsmessage.AResource:
							found = net.IP(body.A[:]).String() == want
						case *dnsmessage.AAAAResource:
							found = net.IP(body.AAAA[:]).String() == want
						}
					}
					if !found {
						t.Fatalf("mode=%s %s %v want=%s answers=%v", mode, name, qt, want, msg.Answers)
					}
				}
			}
			stop()
			for _, tag := range []string{"my_realiplist", "my_fakeiplist"} {
				b, _ := os.ReadFile(filepath.Join(dir, tag+".txt"))
				text := string(b)
				for _, forbidden := range []string{"failed.test", "empty.test", "proxy.test", "domestic.test", "conflict.test"} {
					if strings.Contains(text, forbidden) {
						t.Fatalf("unsafe learning %s: %s", tag, text)
					}
				}
				if mode == "A" && strings.TrimSpace(text) != "" {
					t.Fatalf("domestic answer learned: %s", text)
				}
				if mode == "B" {
					want := "unknown.test"
					if tag == "my_realiplist" {
						want = "learn-direct.test"
					}
					if !strings.Contains(text, want) {
						t.Fatalf("missing trusted learning %s: %s", tag, text)
					}
				}
			}
		})
	}
}

func learningDNSQuery(addr, name string, qt dnsmessage.Type) (dnsmessage.Message, error) {
	var result dnsmessage.Message
	n, err := dnsmessage.NewName(name + ".")
	if err != nil {
		return result, err
	}
	q := dnsmessage.Message{Header: dnsmessage.Header{ID: 123, RecursionDesired: true}, Questions: []dnsmessage.Question{{Name: n, Type: qt, Class: dnsmessage.ClassINET}}}
	b, err := q.Pack()
	if err != nil {
		return result, err
	}
	c, err := net.DialTimeout("udp", addr, 200*time.Millisecond)
	if err != nil {
		return result, err
	}
	defer c.Close()
	c.SetDeadline(time.Now().Add(300 * time.Millisecond))
	if _, err = c.Write(b); err != nil {
		return result, err
	}
	b = make([]byte, 4096)
	count, err := c.Read(b)
	if err != nil {
		return result, err
	}
	if count < 12 || binary.BigEndian.Uint16(b) != 123 {
		return result, fmt.Errorf("invalid DNS reply")
	}
	err = result.Unpack(b[:count])
	return result, err
}

func TestMosDNSLearningReplacementSurvivesCoreRestart(t *testing.T) {
	core := os.Getenv("MSF_TEST_MOSDNS")
	if core == "" {
		t.Skip("set MSF_TEST_MOSDNS for writer/restart regression")
	}
	a := newTestApp(t)
	dir := filepath.Join(a.DataDir, "configs/mosdns/gen")
	plugins := []map[string]any{}
	for _, tag := range []string{"realip", "fakeip"} {
		plugins = append(plugins, map[string]any{"tag": "my_" + tag + "list", "type": "domain_output", "args": map[string]any{"file_stat": filepath.Join(dir, tag+"list.txt"), "file_rule": filepath.Join(dir, tag+"rule.txt"), "dump_interval": 3600, "max_entries": 1000}})
		if err := a.writeTextFile("configs/mosdns/gen/"+tag+"list.txt", "0000000001 2026-09-25 stale.test\n"); err != nil {
			t.Fatal(err)
		}
	}
	b, err := yaml.Marshal(map[string]any{"log": map[string]any{"level": "error"}, "plugins": plugins})
	if err != nil {
		t.Fatal(err)
	}
	config := filepath.Join(t.TempDir(), "config.yaml")
	if err = os.WriteFile(config, b, 0644); err != nil {
		t.Fatal(err)
	}
	installTestMosDNSBinary(t, a, "exec '"+core+"' start -c '"+config+"'\n")
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	if _, err := a.Services.Start(ctx, "mosdns"); err != nil {
		t.Fatal(err)
	}
	defer a.Services.Stop(context.Background(), "mosdns")
	files := map[string][]string{"fakeiprule.txt": {"full:new.test"}, "fakeiplist.txt": mosDNSLearningLines([]string{"new.test"}, "2026-09-26"), "realiprule.txt": nil, "realiplist.txt": nil}
	if err := a.replaceMosDNSLearningFiles(ctx, files); err != nil {
		t.Fatal(err)
	}
	if !a.Services.Status("mosdns").Running {
		t.Fatal("core was not restarted")
	}
	if _, err := a.Services.Stop(ctx, "mosdns"); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"fakeiprule.txt", "fakeiplist.txt", "realiprule.txt", "realiplist.txt"} {
		got, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(got), "stale.test") {
			t.Fatalf("writer restored stale state: %s %s", name, got)
		}
		if strings.HasPrefix(name, "fakeip") && !strings.Contains(string(got), "new.test") {
			t.Fatalf("new state lost after restart: %s %s", name, got)
		}
	}
	// Clearing follows the same stop/replace/start lifecycle, including empty lists.
	if _, err := a.Services.Start(ctx, "mosdns"); err != nil {
		t.Fatal(err)
	}
	for name := range files {
		files[name] = nil
	}
	if err := a.replaceMosDNSLearningFiles(ctx, files); err != nil {
		t.Fatal(err)
	}
	if _, err := a.Services.Stop(ctx, "mosdns"); err != nil {
		t.Fatal(err)
	}
	for name := range files {
		got, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil || len(got) != 0 {
			t.Fatalf("clear did not persist: %s %q %v", name, got, err)
		}
	}
}
