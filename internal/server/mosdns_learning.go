package server

import (
	"context"
	"errors"
	"fmt"
	"net/netip"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Answer parsing must never infer an address from a TTL, CNAME or substring.
func mosDNSAnswerIPs(entry map[string]any) []netip.Addr {
	var out []netip.Addr
	for _, answer := range anySlice(entry["answers"]) {
		var typ, value string
		switch v := answer.(type) {
		case map[string]any:
			typ = strings.ToUpper(stringMapValue(v, "type"))
			value = firstString(v, "data", "value")
		case string:
			fields := strings.Fields(v)
			if len(fields) == 2 {
				typ = strings.TrimSuffix(strings.ToUpper(fields[0]), ":")
				value = fields[1]
			}
		}
		addr, err := netip.ParseAddr(value)
		if err != nil || (typ != "A" && typ != "AAAA") || (typ == "A") != addr.Is4() {
			continue
		}
		out = append(out, addr)
	}
	return out
}

func mosDNSFakeIPPrefixes(cfg SetupConfig) []netip.Prefix {
	cfg.defaults()
	var prefixes []netip.Prefix
	for _, s := range []string{cfg.FakeIPRangeV4, cfg.FakeIPRangeV6} {
		if p, err := netip.ParsePrefix(s); err == nil {
			prefixes = append(prefixes, p.Masked())
		}
	}
	return prefixes
}

func mosDNSIsFakeIP(ip netip.Addr, prefixes []netip.Prefix) bool {
	for _, p := range prefixes {
		if p.Contains(ip) {
			return true
		}
	}
	return false
}

// 0 means insufficient evidence; 3 is a contradictory decision. Learned tags
// alone are not evidence: replaying them would perpetuate the original error.
func mosDNSLearningDecision(entry map[string]any, prefixes []netip.Prefix) uint8 {
	if code := strings.ToUpper(stringMapValue(entry, "response_code")); code != "NOERROR" && code != "0" {
		return 0
	}
	qt := strings.ToUpper(stringMapValue(entry, "query_type"))
	if qt != "A" && qt != "AAAA" {
		return 0
	}
	ips := mosDNSAnswerIPs(entry)
	valid := false
	var decision uint8
	for _, ip := range ips {
		if (qt == "A") != ip.Is4() || ip.IsUnspecified() || ip.IsLoopback() || ip.IsMulticast() {
			continue
		}
		valid = true
		if mosDNSIsFakeIP(ip, prefixes) {
			decision |= 2
		}
	}
	if !valid {
		return 0
	}
	for _, tag := range strings.Split(stringMapValue(entry, "domain_set"), "|") {
		switch strings.TrimSpace(tag) {
		case "白名单", "whitelist", "订阅直连", "订阅直连补充":
			decision |= 1
		case "灰名单", "greylist", "订阅代理", "订阅代理补充":
			decision |= 2
		}
	}
	return decision
}

func mosDNSLearnedDomains(entries []map[string]any, cfg SetupConfig) (fake, real []string) {
	decisions := map[string]uint8{}
	prefixes := mosDNSFakeIPPrefixes(cfg)
	for _, entry := range entries {
		domain := normalizeMosDNSQueryName(stringMapValue(entry, "query_name"))
		// Generate exact domain rules only; do not promote a query to a suffix rule.
		if domain == "" || strings.ContainsAny(domain, " :\t\r\n/") {
			continue
		}
		decisions[domain] |= mosDNSLearningDecision(entry, prefixes)
	}
	f, r := map[string]bool{}, map[string]bool{}
	for d, decision := range decisions {
		if decision == 1 {
			r[d] = true
		}
		if decision == 2 {
			f[d] = true
		}
	}
	return sortedKeys(f), sortedKeys(r)
}

func mosDNSLearningLines(domains []string, date string) []string {
	out := make([]string, 0, len(domains))
	for _, domain := range domains {
		out = append(out, fmt.Sprintf("0000000001 %s %s", date, domain))
	}
	return out
}

func mosDNSExactRules(domains []string) []string {
	out := make([]string, 0, len(domains))
	for _, domain := range domains {
		out = append(out, "full:"+domain)
	}
	return out
}

// domain_output has its own in-memory writer and no import API. Replacing its
// files while it is running would be undone on the next dump or shutdown.
func (a *App) replaceMosDNSLearningFiles(ctx context.Context, files map[string][]string) (err error) {
	running := a.Services != nil && a.Services.Status("mosdns").Running
	if running {
		if _, err = a.Services.stop(ctx, "mosdns", false); err != nil {
			return err
		}
		defer func() {
			restartCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
			defer cancel()
			_, startErr := a.Services.Start(restartCtx, "mosdns")
			err = errors.Join(err, startErr)
		}()
	}
	payload := make(map[string]string, len(files))
	for name, lines := range files {
		content := strings.Join(lines, "\n")
		if content != "" {
			content += "\n"
		}
		payload["configs/mosdns/gen/"+name] = content
	}
	var remove []string
	for _, tag := range mosDNSCachePluginTags {
		remove = append(remove, "configs/mosdns/cache/"+tag+".dump")
	}
	return a.replaceGeneratedConfigFiles(payload, remove)
}

func (a *App) clearMosDNSCacheDumpFiles() error {
	for _, tag := range mosDNSCachePluginTags {
		if err := os.Remove(filepath.Join(a.DataDir, "configs/mosdns/cache", tag+".dump")); err != nil && !os.IsNotExist(err) {
			return err
		}
	}
	return nil
}

// This is a one-time structural migration before managed services start.
// Source lists, hand-written rules, upstreams and switches are kept intact.
func (a *App) ensureMosDNSLearningSafety() error {
	const marker = "data/mosdns-learning-v2"
	if _, err := os.Stat(filepath.Join(a.DataDir, marker)); err == nil {
		return nil
	} else if !os.IsNotExist(err) {
		return err
	}
	for _, name := range []string{"process_v4.yaml", "process_v6.yaml", "not_in_list_ipmatch.yaml", "not_in_list_noleak_v4.yaml", "not_in_list_noleak_v6.yaml"} {
		rel := "mosdns/sub_config/" + name
		content, ok := runtimeTemplateText(rel)
		if !ok {
			return fmt.Errorf("missing template %s", rel)
		}
		if err := a.writeTextFile("configs/"+rel, content); err != nil {
			return err
		}
	}
	// forward_1 contains user-configurable upstreams, so render it with overrides.
	cfg, _ := a.latestSetupConfig()
	cfg.defaults()
	managed, err := a.renderMosDNSManagedFiles(cfg)
	if err != nil {
		return err
	}
	const forward = "configs/mosdns/sub_config/forward_1.yaml"
	if err = a.writeTextFile(forward, managed[forward]); err != nil {
		return err
	}
	for _, name := range []string{"realiplist.txt", "realiprule.txt", "fakeiplist.txt", "fakeiprule.txt", "nov4list.txt", "nov4rule.txt", "nov6list.txt", "nov6rule.txt"} {
		if err = a.writeTextFile("configs/mosdns/gen/"+name, ""); err != nil {
			return err
		}
	}
	if err = a.clearMosDNSCacheDumpFiles(); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(a.DataDir, marker), []byte("2\n"), 0644)
}
