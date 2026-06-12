package config

import (
	"bufio"
	"bytes"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"github.com/trustroots/nostroots/vibe/wrapster/internal/access"
)

func targetsConfigPath(args []string) (string, error) {
	fs := flag.NewFlagSet("wrapster", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	flagPath := fs.String("targets-config", "", "path to conf.toml")
	if err := fs.Parse(args); err != nil {
		return "", err
	}
	if *flagPath != "" {
		return *flagPath, nil
	}
	return os.Getenv("TARGETS_CONFIG_PATH"), nil
}

func loadTargets(path string) (map[string]string, error) {
	fileCfg, err := loadConfigFile(path)
	if err != nil {
		return nil, err
	}
	return fileCfg.Targets, nil
}

type fileConfig struct {
	Targets          map[string]string
	AccessRules      map[string]access.Rule
	ProxyAccessRules []string
	MediaServices    map[string]MediaServiceConfig
	AdminPubkeys     []string
	OwnerPubkey      string
	AdditionalRelays []string
	ProxyGroups      map[string]proxyGroupConfig
	GlobalAccessRule map[string]string
}

type proxyGroupConfig struct {
	URLs                 []string
	AdditionalAccessList []string
}

func loadConfigFile(path string) (fileConfig, error) {
	if path == "" {
		found, err := findUpward(DefaultTargetsConfigPath)
		if err != nil {
			return fileConfig{}, err
		}
		path = found
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return fileConfig{}, fmt.Errorf("read targets config %q: %w", path, err)
	}
	cfg, err := parseConfigTOML(raw)
	if err != nil {
		return fileConfig{}, fmt.Errorf("parse targets config %q: %w", path, err)
	}
	if len(cfg.Targets) == 0 {
		return fileConfig{}, fmt.Errorf("targets config %q must define at least one target", path)
	}
	for key, target := range cfg.Targets {
		cfg.Targets[key] = strings.TrimRight(target, "/")
	}
	return cfg, nil
}

func findUpward(name string) (string, error) {
	dir, err := os.Getwd()
	if err != nil {
		return "", err
	}
	for {
		candidate := filepath.Join(dir, name)
		if _, err := os.Stat(candidate); err == nil {
			return candidate, nil
		} else if !errors.Is(err, os.ErrNotExist) {
			return "", err
		}
		next := filepath.Dir(dir)
		if next == dir {
			return "", fmt.Errorf("%s not found; set TARGETS_CONFIG_PATH or --targets-config", name)
		}
		dir = next
	}
}

func parseTargetsTOML(raw []byte) (map[string]string, error) {
	cfg, err := parseConfigTOML(raw)
	if err != nil {
		return nil, err
	}
	return cfg.Targets, nil
}

func parseConfigTOML(raw []byte) (fileConfig, error) {
	cfg := fileConfig{
		Targets:       map[string]string{},
		AccessRules:   map[string]access.Rule{},
		MediaServices: map[string]MediaServiceConfig{},
		ProxyGroups:   map[string]proxyGroupConfig{},
	}
	scanner := bufio.NewScanner(bytes.NewReader(raw))
	section := ""
	arrayKey := ""
	lineNumber := 0
	for scanner.Scan() {
		lineNumber++
		line := strings.TrimSpace(stripComment(scanner.Text()))
		if line == "" {
			continue
		}
		if arrayKey != "" {
			values, done, err := parseTOMLStringArrayLine(line)
			if err != nil {
				return fileConfig{}, fmt.Errorf("line %d: %w", lineNumber, err)
			}
			if err := applyStringArray(&cfg, section, arrayKey, values); err != nil {
				return fileConfig{}, fmt.Errorf("line %d: %w", lineNumber, err)
			}
			if done {
				arrayKey = ""
			}
			continue
		}
		if strings.HasPrefix(line, "[") {
			if strings.HasPrefix(line, "[[") || !strings.HasSuffix(line, "]") {
				return fileConfig{}, fmt.Errorf("line %d: unsupported TOML table syntax", lineNumber)
			}
			section = strings.TrimSpace(strings.TrimSuffix(strings.TrimPrefix(line, "["), "]"))
			continue
		}
		key, value, ok := strings.Cut(line, "=")
		if !ok {
			return fileConfig{}, fmt.Errorf("line %d: expected key = value", lineNumber)
		}
		key = strings.TrimSpace(key)
		value = strings.TrimSpace(value)
		if key == "" || value == "" {
			return fileConfig{}, fmt.Errorf("line %d: expected non-empty key and value", lineNumber)
		}
		key, err := parseTOMLKey(key)
		if err != nil {
			return fileConfig{}, fmt.Errorf("line %d: %w", lineNumber, err)
		}
		if value == "[" {
			arrayKey = key
			continue
		}
		if strings.HasPrefix(value, "[") {
			values, done, err := parseTOMLStringArrayLine(strings.TrimPrefix(value, "["))
			if err != nil {
				return fileConfig{}, fmt.Errorf("line %d: %w", lineNumber, err)
			}
			if err := applyStringArray(&cfg, section, key, values); err != nil {
				return fileConfig{}, fmt.Errorf("line %d: %w", lineNumber, err)
			}
			if !done {
				arrayKey = key
			}
			continue
		}
		if strings.HasPrefix(value, "{") {
			values, err := parseTOMLInlineStringTable(value)
			if err != nil {
				return fileConfig{}, fmt.Errorf("line %d: %w", lineNumber, err)
			}
			if err := applyStringTable(&cfg, section, key, values); err != nil {
				return fileConfig{}, fmt.Errorf("line %d: %w", lineNumber, err)
			}
			continue
		}
		value, err = parseTOMLString(value)
		if err != nil {
			return fileConfig{}, fmt.Errorf("line %d: %w", lineNumber, err)
		}
		if err := applyStringValue(&cfg, section, key, value); err != nil {
			return fileConfig{}, fmt.Errorf("line %d: %w", lineNumber, err)
		}
	}
	if err := scanner.Err(); err != nil {
		return fileConfig{}, err
	}
	if arrayKey != "" {
		return fileConfig{}, fmt.Errorf("unterminated array for %s", arrayKey)
	}
	if err := cfg.applyGlobalAccessRule(); err != nil {
		return fileConfig{}, err
	}
	if err := cfg.applyProxyGroups(); err != nil {
		return fileConfig{}, err
	}
	if len(cfg.Targets) > 0 && len(cfg.ProxyAccessRules) == 0 {
		cfg.ProxyAccessRules = append([]string{}, cfg.globalAccessRuleNames()...)
	}
	return cfg, nil
}

func parseTargetsListLine(line string, targets map[string]string) (bool, error) {
	values, done, err := parseTOMLStringArrayLine(line)
	if err != nil {
		return false, err
	}
	for _, value := range values {
		if err := addTarget(targets, value); err != nil {
			return false, err
		}
	}
	return done, nil
}

func parseTOMLStringArrayLine(line string) ([]string, bool, error) {
	done := false
	line = strings.TrimSpace(line)
	if strings.HasSuffix(line, "]") {
		done = true
		line = strings.TrimSpace(strings.TrimSuffix(line, "]"))
	}
	line = strings.TrimSpace(line)
	if line == "" {
		return nil, done, nil
	}
	values, err := parseTOMLStringListItems(line)
	if err != nil {
		return nil, false, err
	}
	return values, done, nil
}

func parseTOMLInlineStringTable(value string) (map[string]string, error) {
	value = strings.TrimSpace(value)
	if !strings.HasPrefix(value, "{") || !strings.HasSuffix(value, "}") {
		return nil, fmt.Errorf("expected inline table")
	}
	value = strings.TrimSpace(strings.TrimSuffix(strings.TrimPrefix(value, "{"), "}"))
	out := map[string]string{}
	for value != "" {
		key, rest, err := parseInlineTableKey(value)
		if err != nil {
			return nil, err
		}
		rest = strings.TrimSpace(rest)
		if rest == "" || (rest[0] != '=' && rest[0] != ':') {
			return nil, fmt.Errorf("expected = or : after inline table key")
		}
		rest = strings.TrimSpace(rest[1:])
		if !strings.HasPrefix(rest, "\"") {
			return nil, fmt.Errorf("expected quoted inline table value")
		}
		escaped := false
		end := -1
		for i := 1; i < len(rest); i++ {
			if escaped {
				escaped = false
				continue
			}
			if rest[i] == '\\' {
				escaped = true
				continue
			}
			if rest[i] == '"' {
				end = i
				break
			}
		}
		if end < 0 {
			return nil, fmt.Errorf("expected quoted inline table value")
		}
		parsed, err := parseTOMLString(rest[:end+1])
		if err != nil {
			return nil, err
		}
		out[key] = parsed
		value = strings.TrimSpace(rest[end+1:])
		if value == "" {
			break
		}
		if !strings.HasPrefix(value, ",") {
			return nil, fmt.Errorf("expected comma between inline table entries")
		}
		value = strings.TrimSpace(strings.TrimPrefix(value, ","))
	}
	return out, nil
}

func parseInlineTableKey(value string) (string, string, error) {
	value = strings.TrimSpace(value)
	if strings.HasPrefix(value, "\"") {
		escaped := false
		end := -1
		for i := 1; i < len(value); i++ {
			if escaped {
				escaped = false
				continue
			}
			if value[i] == '\\' {
				escaped = true
				continue
			}
			if value[i] == '"' {
				end = i
				break
			}
		}
		if end < 0 {
			return "", "", fmt.Errorf("expected quoted inline table key")
		}
		key, err := parseTOMLString(value[:end+1])
		if err != nil {
			return "", "", err
		}
		return key, value[end+1:], nil
	}
	end := strings.IndexAny(value, "=:")
	if end < 0 {
		return "", "", fmt.Errorf("expected inline table key")
	}
	key, err := parseTOMLKey(strings.TrimSpace(value[:end]))
	if err != nil {
		return "", "", err
	}
	return key, value[end:], nil
}

func applyStringArray(cfg *fileConfig, section, key string, values []string) error {
	switch {
	case section == "" && (key == "targets" || key == "target" || key == "proxy_targets"):
		for _, value := range values {
			if err := addTarget(cfg.Targets, value); err != nil {
				return err
			}
		}
		return nil
	case section == "admin" && (key == "pubkeys" || key == "owner_npub"):
		cfg.AdminPubkeys = append(cfg.AdminPubkeys, values...)
		return nil
	case section == "" && key == "additional_relays":
		cfg.AdditionalRelays = append(cfg.AdditionalRelays, values...)
		return nil
	case strings.HasPrefix(section, "proxy_group.") && key == "urls":
		name := strings.TrimPrefix(section, "proxy_group.")
		group := cfg.ProxyGroups[name]
		group.URLs = append(group.URLs, values...)
		cfg.ProxyGroups[name] = group
		return nil
	case strings.HasPrefix(section, "proxy_group.") && key == "additional_access_rule":
		name := strings.TrimPrefix(section, "proxy_group.")
		group := cfg.ProxyGroups[name]
		group.AdditionalAccessList = append(group.AdditionalAccessList, values...)
		cfg.ProxyGroups[name] = group
		return nil
	default:
		return fmt.Errorf("unsupported array %s.%s", section, key)
	}
}

func applyStringValue(cfg *fileConfig, section, key, value string) error {
	switch {
	case section == "" && key == "owner_npub":
		pubkey, err := access.NormalizePubkey(value)
		if err != nil {
			return fmt.Errorf("owner_npub is invalid: %w", err)
		}
		cfg.OwnerPubkey = pubkey
		return nil
	case section == "" && (key == "targets" || key == "target" || key == "proxy_targets"):
		return addTarget(cfg.Targets, value)
	case section == "targets":
		cfg.Targets[key] = value
		return nil
	default:
		return fmt.Errorf("unsupported config key %s.%s", section, key)
	}
}

func applyStringTable(cfg *fileConfig, section, key string, values map[string]string) error {
	switch {
	case section == "" && key == "access_rule":
		cfg.GlobalAccessRule = map[string]string{}
		for key, value := range values {
			cfg.GlobalAccessRule[key] = value
		}
		return nil
	default:
		return fmt.Errorf("unsupported table %s.%s", section, key)
	}
}

func (cfg *fileConfig) applyProxyGroups() error {
	for name, group := range cfg.ProxyGroups {
		additionalRuleNames, err := cfg.additionalProxyGroupAccessRules(name, group)
		if err != nil {
			return err
		}
		requiredRules := append([]string{}, cfg.globalAccessRuleNames()...)
		requiredRules = appendUniqueRules(requiredRules, additionalRuleNames...)
		for _, value := range group.URLs {
			if isHTTPURL(value) {
				if err := addTarget(cfg.Targets, value); err != nil {
					return err
				}
				cfg.ProxyAccessRules = appendUniqueRules(cfg.ProxyAccessRules, requiredRules...)
				continue
			}
			service, ok := mediaServiceAlias(value)
			if !ok {
				return fmt.Errorf("proxy_group.%s urls contains unknown service alias %q", name, value)
			}
			if len(requiredRules) > 0 {
				svc := cfg.MediaServices[service]
				svc.AccessRules = appendUniqueRules(svc.AccessRules, requiredRules...)
				cfg.MediaServices[service] = svc
			}
		}
	}
	return nil
}

func (cfg *fileConfig) applyGlobalAccessRule() error {
	if len(cfg.GlobalAccessRule) == 0 {
		return nil
	}
	for key := range cfg.GlobalAccessRule {
		if key != "nip05_domain" {
			return fmt.Errorf("access_rule contains unsupported criterion %q", key)
		}
	}
	domain := strings.TrimSpace(cfg.GlobalAccessRule["nip05_domain"])
	if domain == "" {
		return fmt.Errorf("access_rule nip05_domain is required")
	}
	ruleName := "trustroots_nip05"
	if strings.ToLower(domain) != "trustroots.org" {
		ruleName = "global_nip05"
	}
	rule := cfg.AccessRules[ruleName]
	rule.Type = access.RuleTrustrootsNIP05
	rule.RelayURL = firstRelay(cfg.AdditionalRelays)
	rule.NIP05BaseURL = nip05BaseURLForDomain(domain)
	cfg.AccessRules[ruleName] = rule
	return nil
}

func (cfg *fileConfig) globalAccessRuleNames() []string {
	if len(cfg.GlobalAccessRule) == 0 {
		return nil
	}
	if strings.ToLower(strings.TrimSpace(cfg.GlobalAccessRule["nip05_domain"])) == "trustroots.org" {
		return []string{"trustroots_nip05"}
	}
	return []string{"global_nip05"}
}

func (cfg *fileConfig) additionalProxyGroupAccessRules(name string, group proxyGroupConfig) ([]string, error) {
	ruleNames := []string{}
	for _, value := range group.AdditionalAccessList {
		switch value {
		case access.RuleNostrFollow:
			if strings.TrimSpace(cfg.OwnerPubkey) == "" {
				return nil, fmt.Errorf("owner_npub is required for nostr_follow access")
			}
			ruleName := "media_owner_follows"
			rule := cfg.AccessRules[ruleName]
			rule.Type = access.RuleNostrFollow
			rule.RelayURL = firstRelay(cfg.AdditionalRelays)
			rule.OwnerPubkey = cfg.OwnerPubkey
			rule.Relationship = "owner_follows_user"
			cfg.AccessRules[ruleName] = rule
			ruleNames = appendUniqueRules(ruleNames, ruleName)
		default:
			return nil, fmt.Errorf("proxy_group.%s additional_access_rule contains unsupported rule %q", name, value)
		}
	}
	return ruleNames, nil
}

func appendUniqueRules(rules []string, values ...string) []string {
	seen := map[string]struct{}{}
	out := make([]string, 0, len(rules)+len(values))
	for _, value := range append(rules, values...) {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	return out
}

func firstRelay(relays []string) string {
	for _, relay := range relays {
		if strings.TrimSpace(relay) != "" {
			return strings.TrimSpace(relay)
		}
	}
	return access.DefaultRelayURL
}

func nip05BaseURLForDomain(domain string) string {
	domain = strings.TrimSpace(strings.ToLower(domain))
	if domain == "trustroots.org" {
		return "https://www.trustroots.org/.well-known/nostr.json"
	}
	return "https://" + domain + "/.well-known/nostr.json"
}

func mediaServiceAlias(value string) (string, bool) {
	switch value {
	case "wireguard_jellyfin":
		return "jellyfin", true
	case "wireguard_plex":
		return "plex", true
	default:
		return "", false
	}
}

func isHTTPURL(value string) bool {
	u, err := url.Parse(value)
	return err == nil && u.Host != "" && (u.Scheme == "http" || u.Scheme == "https")
}

func addTarget(targets map[string]string, value string) error {
	key, err := routeKeyFromTarget(value)
	if err != nil {
		return err
	}
	if _, exists := targets[key]; exists {
		return fmt.Errorf("duplicate derived target key %q", key)
	}
	targets[key] = value
	return nil
}

func parseTOMLStringListItems(line string) ([]string, error) {
	values := []string{}
	for {
		line = strings.TrimSpace(line)
		if line == "" {
			return values, nil
		}
		if !strings.HasPrefix(line, "\"") {
			return nil, fmt.Errorf("expected quoted string")
		}
		escaped := false
		end := -1
		for i := 1; i < len(line); i++ {
			if escaped {
				escaped = false
				continue
			}
			if line[i] == '\\' {
				escaped = true
				continue
			}
			if line[i] == '"' {
				end = i
				break
			}
		}
		if end < 0 {
			return nil, fmt.Errorf("expected quoted string")
		}
		value, err := parseTOMLString(line[:end+1])
		if err != nil {
			return nil, err
		}
		values = append(values, value)
		line = strings.TrimSpace(line[end+1:])
		if line == "" {
			return values, nil
		}
		if !strings.HasPrefix(line, ",") {
			return nil, fmt.Errorf("expected comma between target URLs")
		}
		line = strings.TrimPrefix(line, ",")
	}
}

func routeKeyFromTarget(value string) (string, error) {
	u, err := url.Parse(value)
	if err != nil || u.Host == "" {
		return "", fmt.Errorf("target URL %q must include a host", value)
	}
	host := strings.ToLower(u.Hostname())
	host = strings.TrimPrefix(host, "www.")
	if host == "trustroots.org" {
		return "trustroots", nil
	}
	return host, nil
}

func stripComment(line string) string {
	inString := false
	escaped := false
	for i, r := range line {
		if escaped {
			escaped = false
			continue
		}
		if r == '\\' && inString {
			escaped = true
			continue
		}
		if r == '"' {
			inString = !inString
			continue
		}
		if r == '#' && !inString {
			return line[:i]
		}
	}
	return line
}

func parseTOMLKey(key string) (string, error) {
	key = strings.TrimSpace(key)
	if strings.HasPrefix(key, "\"") {
		return parseTOMLString(key)
	}
	if strings.ContainsAny(key, " \t\r\n") {
		return "", fmt.Errorf("bare target keys must not contain whitespace")
	}
	if key == "" {
		return "", fmt.Errorf("target key must not be empty")
	}
	return key, nil
}

func parseTOMLString(value string) (string, error) {
	if !strings.HasPrefix(value, "\"") || !strings.HasSuffix(value, "\"") || len(value) < 2 {
		return "", fmt.Errorf("expected quoted string")
	}
	value = strings.TrimPrefix(strings.TrimSuffix(value, "\""), "\"")
	replacer := strings.NewReplacer(`\"`, `"`, `\\`, `\`)
	return replacer.Replace(value), nil
}

func validateRouteKey(value string) error {
	if value == "" || strings.Contains(value, "/") || strings.ContainsAny(value, " \t\r\n") {
		return fmt.Errorf("target key %q must be a non-empty path segment", value)
	}
	return nil
}

func validateTarget(name, value string) error {
	u, err := url.Parse(value)
	if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") {
		return fmt.Errorf("%s must be an http:// or https:// URL", name)
	}
	return nil
}

func validateOrigin(origin string) error {
	u, err := url.Parse(origin)
	if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") || (u.Path != "" && u.Path != "/") || u.RawQuery != "" || u.Fragment != "" {
		return fmt.Errorf("ALLOWED_ORIGINS contains invalid origin %q", origin)
	}
	return nil
}
