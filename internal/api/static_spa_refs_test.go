package api

import (
	"os"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// TestSPAJSReferencesResolve scans the inline <script> in static/index.html
// and asserts that every bareword function call resolves to either a definition
// in the same file or a known browser/JS global. This catches typos like the
// "refreshTasks() vs refresh()" bug shipped in 04690ee, where a ReferenceError
// only surfaced when the user clicked the button — never caught by Go tests.
func TestSPAJSReferencesResolve(t *testing.T) {
	data, err := os.ReadFile("static/index.html")
	if err != nil {
		t.Fatalf("read static/index.html: %v", err)
	}

	js := extractInlineScript(string(data))
	if js == "" {
		t.Fatal("expected an inline <script> block in static/index.html")
	}
	code := stripJSStringsAndComments(js)

	defs := collectJSDefinitions(code)
	calls := collectJSCalls(code)

	// Also fold every function name referenced from an inline onclick="X(...)"
	// attribute into the call set. Those handlers are evaluated in the page
	// scope; an undefined function name there throws on click.
	for _, name := range extractInlineHandlerCalls(string(data)) {
		calls[name] = true
	}

	var missing []string
	for name := range calls {
		if defs[name] || jsGlobalsAllowlist[name] || jsKeywordCallable[name] {
			continue
		}
		missing = append(missing, name)
	}
	if len(missing) > 0 {
		sort.Strings(missing)
		t.Fatalf("SPA JS references these names with no matching definition or known global:\n  %s\n\n"+
			"If they're legitimate (e.g., a new browser global), add to jsGlobalsAllowlist in static_spa_refs_test.go.\n"+
			"If they're typos (e.g., refreshTasks instead of refresh), fix the call site.",
			strings.Join(missing, ", "))
	}
}

// extractInlineScript returns the body of the first inline (no src attribute)
// <script>...</script> block in the HTML, or "" if none is found.
func extractInlineScript(html string) string {
	openRE := regexp.MustCompile(`(?i)<script(\s[^>]*)?>`)
	closeRE := regexp.MustCompile(`(?i)</script>`)
	idx := openRE.FindStringIndex(html)
	for idx != nil {
		tagAttrs := html[idx[0]:idx[1]]
		bodyStart := idx[1]
		closeIdx := closeRE.FindStringIndex(html[bodyStart:])
		if closeIdx == nil {
			return ""
		}
		bodyEnd := bodyStart + closeIdx[0]
		// Skip <script src="...">; those are externally loaded and not in scope.
		if !strings.Contains(strings.ToLower(tagAttrs), " src=") {
			return html[bodyStart:bodyEnd]
		}
		rest := bodyEnd + (closeIdx[1] - closeIdx[0])
		nextIdx := openRE.FindStringIndex(html[rest:])
		if nextIdx == nil {
			return ""
		}
		idx = []int{rest + nextIdx[0], rest + nextIdx[1]}
	}
	return ""
}

// stripJSStringsAndComments returns the JS source with strings, template-literal
// bodies (not their ${...} interpolations), line comments, and block comments
// replaced with spaces. Newlines are preserved so positions remain stable.
//
// Known limitation: regex literals are NOT stripped. A pattern containing `\/`
// near its closing delimiter (e.g. `/https:\/\//i`) leaves consecutive `/`
// characters that this pass would interpret as a `//` line comment, blanking
// from there to end-of-line. The SPA's existing regex literals are alone on
// their lines so this currently produces no false negatives. If a future
// regex+call appears on one line, refactor the line OR add explicit
// regex-literal detection here.
func stripJSStringsAndComments(js string) string {
	b := []byte(js)
	out := append([]byte(nil), b...)
	n := len(b)

	type frame struct {
		kind  int // 0 = code (top-level), 1 = template literal body, 2 = template ${...} interpolation
		depth int // brace depth for interpolation
	}
	stack := []frame{{kind: 0}}

	blank := func(i int) {
		if b[i] != '\n' {
			out[i] = ' '
		}
	}

	i := 0
	for i < n {
		top := len(stack) - 1
		kind := stack[top].kind
		ch := b[i]

		if kind == 1 {
			// inside `...` template body
			if ch == '`' {
				blank(i)
				i++
				stack = stack[:top]
				continue
			}
			if ch == '$' && i+1 < n && b[i+1] == '{' {
				blank(i)
				blank(i + 1)
				i += 2
				stack = append(stack, frame{kind: 2, depth: 1})
				continue
			}
			if ch == '\\' && i+1 < n {
				blank(i)
				blank(i + 1)
				i += 2
				continue
			}
			blank(i)
			i++
			continue
		}

		// code mode (top-level or inside ${...})
		if ch == '/' && i+1 < n {
			if b[i+1] == '/' {
				for i < n && b[i] != '\n' {
					blank(i)
					i++
				}
				continue
			}
			if b[i+1] == '*' {
				blank(i)
				blank(i + 1)
				i += 2
				for i+1 < n && (b[i] != '*' || b[i+1] != '/') {
					blank(i)
					i++
				}
				if i+1 < n {
					blank(i)
					blank(i + 1)
					i += 2
				}
				continue
			}
		}
		if ch == '\'' || ch == '"' {
			quote := ch
			blank(i)
			i++
			for i < n && b[i] != quote {
				if b[i] == '\\' && i+1 < n {
					blank(i)
					blank(i + 1)
					i += 2
					continue
				}
				blank(i)
				i++
			}
			if i < n {
				blank(i)
				i++
			}
			continue
		}
		if ch == '`' {
			blank(i)
			i++
			stack = append(stack, frame{kind: 1})
			continue
		}
		if kind == 2 {
			if ch == '{' {
				stack[top].depth++
			} else if ch == '}' {
				stack[top].depth--
				if stack[top].depth == 0 {
					blank(i)
					i++
					stack = stack[:top]
					continue
				}
			}
		}
		i++
	}
	return string(out)
}

var jsIdentRE = regexp.MustCompile(`[A-Za-z_$][A-Za-z0-9_$]*`)

// collectJSDefinitions returns the set of every name introduced as a binding,
// function, class, or parameter anywhere in the JS source. The pass deliberately
// over-collects (no scope tracking) to keep false positives in the test low.
func collectJSDefinitions(code string) map[string]bool {
	defs := map[string]bool{}
	add := func(name string) {
		if name != "" {
			defs[name] = true
		}
	}

	patterns := []*regexp.Regexp{
		regexp.MustCompile(`\b(?:async\s+)?function\s*\*?\s*([A-Za-z_$][A-Za-z0-9_$]*)\s*\(`),
		regexp.MustCompile(`\b(?:const|let|var)\s+([A-Za-z_$][A-Za-z0-9_$]*)`),
		regexp.MustCompile(`\bclass\s+([A-Za-z_$][A-Za-z0-9_$]*)`),
		regexp.MustCompile(`\bfor\s*\(\s*(?:let|var|const)\s+([A-Za-z_$][A-Za-z0-9_$]*)`),
		regexp.MustCompile(`\bcatch\s*\(\s*([A-Za-z_$][A-Za-z0-9_$]*)\s*\)`),
		// single-param arrow: `name => ...`
		regexp.MustCompile(`(?:^|[^A-Za-z0-9_$.])([A-Za-z_$][A-Za-z0-9_$]*)\s*=>`),
	}
	for _, re := range patterns {
		for _, m := range re.FindAllStringSubmatch(code, -1) {
			add(m[1])
		}
	}

	// function NAME(p1, p2, ...) → parameters
	fnParams := regexp.MustCompile(`\b(?:async\s+)?function\s*\*?\s*[A-Za-z_$]?[A-Za-z0-9_$]*\s*\(([^)]*)\)`)
	for _, m := range fnParams.FindAllStringSubmatch(code, -1) {
		for _, id := range jsIdentRE.FindAllString(m[1], -1) {
			add(id)
		}
	}

	// (p1, p2, ...) => — arrow params (only matches when nothing nested in parens)
	arrowParens := regexp.MustCompile(`\(([^()]*)\)\s*=>`)
	for _, m := range arrowParens.FindAllStringSubmatch(code, -1) {
		for _, id := range jsIdentRE.FindAllString(m[1], -1) {
			add(id)
		}
	}

	// const|let|var { ... } and const|let|var [ ... ] destructuring
	destrObj := regexp.MustCompile(`\b(?:const|let|var)\s*\{([^}]+)\}`)
	for _, m := range destrObj.FindAllStringSubmatch(code, -1) {
		for _, id := range jsIdentRE.FindAllString(m[1], -1) {
			add(id)
		}
	}
	destrArr := regexp.MustCompile(`\b(?:const|let|var)\s*\[([^\]]+)\]`)
	for _, m := range destrArr.FindAllStringSubmatch(code, -1) {
		for _, id := range jsIdentRE.FindAllString(m[1], -1) {
			add(id)
		}
	}

	// Class method names: `methodName(...)` inside class bodies look like calls
	// but are definitions. Detected loosely by scanning `class X { ... }` blocks.
	classBody := regexp.MustCompile(`\bclass\s+[A-Za-z_$][A-Za-z0-9_$]*\s*(?:extends\s+[A-Za-z_$][A-Za-z0-9_$.]*\s*)?\{`)
	for _, idx := range classBody.FindAllStringIndex(code, -1) {
		// Walk forward, brace-matching, collect every IDENT( inside.
		depth := 1
		j := idx[1]
		for j < len(code) && depth > 0 {
			switch code[j] {
			case '{':
				depth++
			case '}':
				depth--
			}
			j++
		}
		body := code[idx[1] : j-1]
		methodRE := regexp.MustCompile(`(?:^|[\n;{}])\s*(?:static\s+|async\s+|get\s+|set\s+|\*\s*)*([A-Za-z_$][A-Za-z0-9_$]*)\s*\(`)
		for _, m := range methodRE.FindAllStringSubmatch(body, -1) {
			add(m[1])
		}
	}

	return defs
}

// collectJSCalls returns the set of bareword identifiers used in a function-call
// position (`NAME(`), excluding method calls (`.NAME(`) and language keywords
// that take a paren-list (`if`, `for`, `while`, `switch`, `catch`, ...).
func collectJSCalls(code string) map[string]bool {
	calls := map[string]bool{}
	callRE := regexp.MustCompile(`(?:^|[^A-Za-z0-9_$.])([A-Za-z_$][A-Za-z0-9_$]*)\s*\(`)
	for _, m := range callRE.FindAllStringSubmatch(code, -1) {
		name := m[1]
		if jsKeywordCallable[name] {
			continue
		}
		calls[name] = true
	}
	return calls
}

// extractInlineHandlerCalls returns every function name appearing as a bareword
// call inside an `on*="..."` HTML attribute (e.g. `onclick="a(); b()"` returns
// both `a` and `b`). Only double-quoted attribute values are recognized — the
// SPA exclusively uses double quotes, and supporting single quotes would
// require attribute-aware HTML parsing rather than a regex pass.
func extractInlineHandlerCalls(html string) []string {
	// Require whitespace before `on` so substrings of unrelated attribute names
	// (e.g. `content="..."` would match `ontent`, `aria-controls="x"` would
	// match `ontrols`) don't false-match into the handler set.
	attrRE := regexp.MustCompile(`(?:^|\s)(on[a-z]+)="([^"]*)"`)
	callRE := regexp.MustCompile(`(?:^|[^A-Za-z0-9_$.])([A-Za-z_$][A-Za-z0-9_$]*)\s*\(`)
	out := make([]string, 0)
	for _, attr := range attrRE.FindAllStringSubmatch(html, -1) {
		for _, m := range callRE.FindAllStringSubmatch(attr[2], -1) {
			out = append(out, m[1])
		}
	}
	return out
}

// TestSPAStaticAnalyzer_DetectsTypoInSyntheticScript exercises the static
// analyzer on a minimal hand-rolled HTML/JS snippet. This pins the contract
// independently of static/index.html: even if the SPA is rewritten end-to-end,
// the typo-catching behaviour must keep working.
func TestSPAStaticAnalyzer_DetectsTypoInSyntheticScript(t *testing.T) {
	tests := []struct {
		name     string
		html     string
		wantBad  []string
		wantGood []string
	}{
		{
			name: "undefined call inside function body fails",
			html: `<script>
function pruneCompleted() {
  refresh();
  refreshTasks();
}
function refresh() {}
</script>`,
			wantBad: []string{"refreshTasks"},
		},
		{
			name: "undefined call from inline onclick handler fails",
			html: `<button onclick="doStuff()">go</button>
<script>function realThing() {}</script>`,
			wantBad: []string{"doStuff"},
		},
		{
			name: "calls inside string literals are ignored",
			html: `<script>
const msg = "refresh() was called";
const code = 'doStuff()';
const tmpl = ` + "`undefinedFn() is fine in a template body`" + `;
function known() {}
known();
</script>`,
			wantGood: []string{"known"},
		},
		{
			name: "calls inside line and block comments are ignored",
			html: `<script>
// notARealFn() in a comment
/* alsoNotReal(); */
function only() {}
only();
</script>`,
			wantGood: []string{"only"},
		},
		{
			name: "method calls do not count as bareword calls",
			html: `<script>
const arr = [];
arr.push(1);
arr.map(x => x);
</script>`,
		},
		{
			name: "JS globals do not need a local definition",
			html: `<script>
parseInt("3");
setTimeout(() => {}, 10);
fetch("/api/x");
</script>`,
		},
		{
			name:    "template literal interpolation is still scanned",
			html:    "<script>function ok(){} const s = `hello ${ok()}`; const bad = `boom ${ohNo()}`;</script>",
			wantBad: []string{"ohNo"},
		},
		{
			name: "multi-line destructuring registers all bindings",
			html: `<script>
const {
  alpha,
  bravo,
} = obj;
const [
  one,
  two,
] = arr;
alpha(); bravo(); one(); two();
</script>`,
			wantGood: []string{"alpha", "bravo", "one", "two"},
		},
		{
			name: "multi-call onclick handler flags every undefined name",
			html: `<button onclick="known(); typoFn()">go</button>
<script>function known() {}</script>`,
			wantBad: []string{"typoFn"},
		},
		{
			name: "non-handler attributes whose names contain 'on' do not false-match",
			html: `<meta name="content" content="width=device-width">
<button aria-controls="ios-share-help">?</button>
<script>function only() {} only();</script>`,
			wantGood: []string{"only"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			js := extractInlineScript(tc.html)
			code := stripJSStringsAndComments(js)
			defs := collectJSDefinitions(code)
			calls := collectJSCalls(code)
			for _, name := range extractInlineHandlerCalls(tc.html) {
				calls[name] = true
			}
			missing := map[string]bool{}
			for name := range calls {
				if defs[name] || jsGlobalsAllowlist[name] || jsKeywordCallable[name] {
					continue
				}
				missing[name] = true
			}
			for _, want := range tc.wantBad {
				if !missing[want] {
					t.Errorf("expected %q to be flagged as undefined; missing=%v", want, missing)
				}
			}
			for _, want := range tc.wantGood {
				if !defs[want] {
					t.Errorf("expected %q to be recognized as defined; defs=%v", want, defs)
				}
				if missing[want] {
					t.Errorf("expected %q to NOT be flagged as undefined; missing=%v", want, missing)
				}
			}
			// Tightness: nothing should land in `missing` other than the
			// names explicitly listed in wantBad. Without this, a leak
			// (e.g. a string-literal call slipping past the stripper)
			// would silently pass the test.
			expectedBad := map[string]bool{}
			for _, n := range tc.wantBad {
				expectedBad[n] = true
			}
			for got := range missing {
				if !expectedBad[got] {
					t.Errorf("unexpected name %q leaked into missing set; full set=%v", got, missing)
				}
			}
		})
	}
}

// TestExtractInlineScript covers the script-extraction edge cases.
func TestExtractInlineScript(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{"basic", "<script>foo();</script>", "foo();"},
		{"attrs", "<script type=\"module\">bar();</script>", "bar();"},
		{"skips src and finds inline", `<script src="/vendor/xterm.js"></script><script>baz();</script>`, "baz();"},
		{"missing block returns empty", "<p>no scripts here</p>", ""},
		{"unclosed returns empty", "<script>foo();", ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := extractInlineScript(tc.in)
			if got != tc.want {
				t.Errorf("got %q, want %q", got, tc.want)
			}
		})
	}
}

// TestStripJSStringsAndComments_BlanksBracesInsideStrippedContexts pins the
// invariant that the class-body brace walker depends on: braces inside strings,
// template literal bodies, and comments must be blanked to spaces so they don't
// unbalance the `{`/`}` depth count in collectJSDefinitions's class-body
// scanner.
func TestStripJSStringsAndComments_BlanksBracesInsideStrippedContexts(t *testing.T) {
	tests := []struct {
		name string
		in   string
	}{
		{"double-quoted string with braces", `var s = "a{b}c{d}e";`},
		{"single-quoted string with braces", `var s = 'a{b}c{d}e';`},
		{"template body with braces", "var t = `lit{erals}{stay}`;"},
		{"line comment with braces", "// {open} {close}\n"},
		{"block comment with braces", "/* a{b}c{d}e */"},
		{"escaped quote inside string with brace", `var s = "a\"{b}\"c";`},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			out := stripJSStringsAndComments(tc.in)
			if strings.ContainsAny(out, "{}") {
				t.Errorf("braces leaked through stripper:\n  in:  %q\n  out: %q", tc.in, out)
			}
		})
	}
}

// TestStripJSStringsAndComments_PreservesNewlinesAndPositions covers the
// invariants the analyzer relies on: replacement is character-positional and
// newlines stay intact so source lines remain stable.
func TestStripJSStringsAndComments_PreservesNewlinesAndPositions(t *testing.T) {
	in := "a();\n'b()';\n`c()`;\n//d();\n/*e()*/\nf();"
	out := stripJSStringsAndComments(in)
	if len(out) != len(in) {
		t.Fatalf("length changed: got %d, want %d", len(out), len(in))
	}
	if strings.Count(out, "\n") != strings.Count(in, "\n") {
		t.Fatalf("newline count changed: got %d, want %d",
			strings.Count(out, "\n"), strings.Count(in, "\n"))
	}
	// `a()` and `f()` are code, must remain present
	if !strings.Contains(out, "a()") {
		t.Errorf("code call a() was stripped: %q", out)
	}
	if !strings.Contains(out, "f()") {
		t.Errorf("code call f() was stripped: %q", out)
	}
	// `b()` and `c()` were inside string/template body, must be blanked
	if strings.Contains(out, "b()") {
		t.Errorf("string content was NOT stripped: %q", out)
	}
	if strings.Contains(out, "c()") {
		t.Errorf("template content was NOT stripped: %q", out)
	}
	// `d()` and `e()` were inside comments, must be blanked
	if strings.Contains(out, "d()") {
		t.Errorf("line comment was NOT stripped: %q", out)
	}
	if strings.Contains(out, "e()") {
		t.Errorf("block comment was NOT stripped: %q", out)
	}
}

// jsKeywordCallable: identifiers that look like callable but are control-flow
// keywords or grammatical constructs taking a parenthesized clause.
var jsKeywordCallable = map[string]bool{
	"if": true, "else": true, "for": true, "while": true, "do": true,
	"switch": true, "case": true, "default": true,
	"return": true, "function": true, "async": true, "await": true,
	"var": true, "let": true, "const": true, "class": true,
	"new": true, "delete": true, "typeof": true, "instanceof": true, "in": true, "of": true,
	"try": true, "catch": true, "finally": true, "throw": true,
	"void": true, "yield": true, "this": true, "super": true,
	"true": true, "false": true, "null": true, "undefined": true,
	"static": true, "get": true, "set": true, "import": true, "export": true,
	"break": true, "continue": true,
}

// jsGlobalsAllowlist: browser/JS globals that are not defined in our source
// but are guaranteed by the runtime. Add new entries here when a legitimate
// global is flagged.
var jsGlobalsAllowlist = map[string]bool{
	// Built-in constructors and namespaces
	"Array": true, "ArrayBuffer": true, "BigInt": true, "Boolean": true,
	"Date": true, "Error": true, "Event": true, "Function": true, "Image": true,
	"JSON": true, "Map": true, "Math": true, "Number": true, "Object": true,
	"Promise": true, "Proxy": true, "Reflect": true, "RegExp": true,
	"Set": true, "String": true, "Symbol": true, "WeakMap": true, "WeakSet": true,
	"Uint8Array": true, "Uint8ClampedArray": true, "Uint16Array": true, "Uint32Array": true,
	"Int8Array": true, "Int16Array": true, "Int32Array": true,
	"Float32Array": true, "Float64Array": true,
	"DataView": true, "TextEncoder": true, "TextDecoder": true,

	// Web APIs
	"AbortController": true, "AbortSignal": true, "Blob": true, "File": true,
	"FileList": true, "FileReader": true, "FormData": true,
	"Headers": true, "Request": true, "Response": true, "URL": true,
	"URLSearchParams": true, "EventSource": true, "WebSocket": true,
	"XMLHttpRequest": true, "MessageChannel": true, "BroadcastChannel": true,
	"CustomEvent": true, "MouseEvent": true, "KeyboardEvent": true,
	"FocusEvent": true, "TouchEvent": true, "PointerEvent": true,
	"WheelEvent": true, "InputEvent": true, "DragEvent": true, "ClipboardEvent": true,
	"MutationObserver": true, "IntersectionObserver": true, "ResizeObserver": true,
	"PerformanceObserver": true, "Notification": true, "ServiceWorker": true,
	"ServiceWorkerRegistration": true, "PushSubscription": true,

	// Global functions
	"setTimeout": true, "setInterval": true, "clearTimeout": true, "clearInterval": true,
	"requestAnimationFrame": true, "cancelAnimationFrame": true,
	"requestIdleCallback": true, "cancelIdleCallback": true,
	"parseInt": true, "parseFloat": true, "isNaN": true, "isFinite": true,
	"encodeURIComponent": true, "decodeURIComponent": true,
	"encodeURI": true, "decodeURI": true,
	"escape": true, "unescape": true, "atob": true, "btoa": true,
	"confirm": true, "alert": true, "prompt": true, "fetch": true,
	"eval": true, "structuredClone": true, "queueMicrotask": true,
	"reportError": true, "getComputedStyle": true, "matchMedia": true,
	"postMessage": true, "addEventListener": true, "removeEventListener": true,
	"dispatchEvent": true,

	// xterm.js loaded via <script src> from vendor — its globals end up on window
	"Terminal": true, "FitAddon": true,

	// IIFE syntax `function(){}` (anonymous) — the regex sees a call-site `function(`
	// even though that token is a keyword. Already filtered via jsKeywordCallable.
}
