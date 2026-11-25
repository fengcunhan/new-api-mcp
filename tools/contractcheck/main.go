// Command contractcheck compares the routes and scopes every tool declares
// with new-api's source, so a new-api upgrade that moves a route or changes a
// scope is caught before deployment.
//
//	go run ./tools/contractcheck -newapi ../new-api
package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	"github.com/fengcunhan/new-api-mcp/internal/catalog"
	"github.com/fengcunhan/new-api-mcp/internal/newapi"
)

func main() {
	root := flag.String("newapi", "../new-api", "path to a new-api checkout")
	flag.Parse()
	rules, err := Load(*root)
	if err != nil {
		fmt.Fprintln(os.Stderr, "contractcheck:", err)
		os.Exit(2)
	}
	var routes []newapi.Route
	for _, d := range catalog.All() {
		for _, r := range d.Spec.Routes {
			if !slices.Contains(routes, r) {
				routes = append(routes, r)
			}
		}
	}
	problems := Check(routes, rules)
	for _, p := range problems {
		fmt.Println("FAIL", p)
	}
	fmt.Printf("checked %d routes against %d new-api rules: %d problems\n", len(routes), len(rules), len(problems))
	if len(problems) > 0 {
		os.Exit(1)
	}
}

// Rule is new-api's access token rule for one route: a scope, "any" (every
// token, the handler checks) or "session" (browser sign-in only).
type Rule struct {
	Kind  string
	Scope string
}

var (
	tableRule      = regexp.MustCompile(`"(GET|POST|PUT|PATCH|DELETE) (/api/[^"]*)":\s*accessToken(?:ScopeRule\("([^"]+)"\)|(AnyRule)|(SessionRule)|ScopeRule\(service\.AccessTokenScopeOf\(authz\.(\w+)\)\))`)
	permissionLine = regexp.MustCompile(`method: http\.Method(\w+), path: "([^"]*)", permission: authz\.(\w+)`)
	handlePerm     = regexp.MustCompile(`http\.Method(\w+), "([^"]+)", authz\.(\w+)`)
	anonymousRoute = regexp.MustCompile(`apiRouter\.(GET|POST)\("(/[^"]*)", controller\.`)
)

// permissionScope turns an authz permission name into its scope, for example
// ChannelSensitiveWrite into channel:sensitive_write.
func permissionScope(name string) string {
	for _, resource := range []string{"TaskPlugin", "Channel", "Audit"} {
		action, ok := strings.CutPrefix(name, resource)
		if ok {
			return snake(resource) + ":" + snake(action)
		}
	}
	return "unknown:" + name
}

func snake(s string) string {
	var b strings.Builder
	for i, r := range s {
		if r >= 'A' && r <= 'Z' {
			if i > 0 {
				b.WriteByte('_')
			}
			r += 'a' - 'A'
		}
		b.WriteRune(r)
	}
	return b.String()
}

// Load reads every route rule from a new-api checkout.
func Load(root string) (map[string]Rule, error) {
	rules := map[string]Rule{}
	tables, err := filepath.Glob(filepath.Join(root, "middleware", "access_token_routes*.go"))
	if err != nil || len(tables) == 0 {
		return nil, fmt.Errorf("no middleware/access_token_routes*.go under %s", root)
	}
	for _, file := range tables {
		if strings.HasSuffix(file, "_test.go") {
			continue
		}
		src, err := os.ReadFile(file)
		if err != nil {
			return nil, err
		}
		for _, m := range tableRule.FindAllStringSubmatch(string(src), -1) {
			key := m[1] + " " + m[2]
			switch {
			case m[3] != "":
				rules[key] = Rule{Kind: "scope", Scope: m[3]}
			case m[4] != "":
				rules[key] = Rule{Kind: "any"}
			case m[5] != "":
				rules[key] = Rule{Kind: "session"}
			case m[6] != "":
				rules[key] = Rule{Kind: "scope", Scope: permissionScope(m[6])}
			}
		}
	}

	channelRouter, err := os.ReadFile(filepath.Join(root, "router", "channel-router.go"))
	if err != nil {
		return nil, err
	}
	for _, m := range permissionLine.FindAllStringSubmatch(string(channelRouter), -1) {
		path := "/api/channel" + m[2]
		if m[2] == "/" {
			path = "/api/channel/"
		}
		rules[strings.ToUpper(m[1])+" "+path] = Rule{Kind: "scope", Scope: permissionScope(m[3])}
	}

	apiRouter, err := os.ReadFile(filepath.Join(root, "router", "api-router.go"))
	if err != nil {
		return nil, err
	}
	for _, m := range handlePerm.FindAllStringSubmatch(string(apiRouter), -1) {
		rules[strings.ToUpper(m[1])+" /api"+m[2]] = Rule{Kind: "scope", Scope: permissionScope(m[3])}
	}
	for _, m := range anonymousRoute.FindAllStringSubmatch(string(apiRouter), -1) {
		key := m[1] + " /api" + m[2]
		if _, guarded := rules[key]; !guarded {
			rules[key] = Rule{Kind: "anonymous"}
		}
	}
	return rules, nil
}

// Check reports every declared route that new-api does not serve with the
// same scope.
func Check(routes []newapi.Route, rules map[string]Rule) []string {
	var problems []string
	for _, r := range routes {
		rule, ok := rules[r.String()]
		switch {
		case !ok:
			problems = append(problems, fmt.Sprintf("%s: not found in new-api", r))
		case r.Scope == "" && rule.Kind != "anonymous":
			problems = append(problems, fmt.Sprintf("%s: declared anonymous, new-api has %s %s", r, rule.Kind, rule.Scope))
		case r.Scope == "":
		case rule.Kind == "session":
			problems = append(problems, fmt.Sprintf("%s: new-api accepts only browser sessions here", r))
		case rule.Kind == "scope" && rule.Scope != r.Scope:
			problems = append(problems, fmt.Sprintf("%s: declared scope %s, new-api requires %s", r, r.Scope, rule.Scope))
		}
	}
	return problems
}
