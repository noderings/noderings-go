// Command gen generates the NodeRings Go SDK's models and services from the
// OpenAPI description in api/openapi.json. Run it with `go generate ./...`.
package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"go/format"
	"log"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"unicode"
)

// Tags left out of the SDK: inbound payment-provider webhooks and staff-only
// enforcement endpoints, which no SDK user can call.
var excludedTags = map[string]bool{
	"WebhookService":     true,
	"EnforcementService": true,
}

type spec struct {
	Paths      map[string]map[string]*operation `json:"paths"`
	Components struct {
		Schemas       map[string]*schema `json:"schemas"`
		RequestBodies map[string]struct {
			Content map[string]struct {
				Schema *schema `json:"schema"`
			} `json:"content"`
		} `json:"requestBodies"`
	} `json:"components"`
}

type operation struct {
	OperationID string       `json:"operationId"`
	Summary     string       `json:"summary"`
	Description string       `json:"description"`
	Tags        []string     `json:"tags"`
	Parameters  []*parameter `json:"parameters"`
	RequestBody *struct {
		Ref     string `json:"$ref"`
		Content map[string]struct {
			Schema *schema `json:"schema"`
		} `json:"content"`
	} `json:"requestBody"`
	Responses map[string]struct {
		Content map[string]struct {
			Schema *schema `json:"schema"`
		} `json:"content"`
	} `json:"responses"`
}

type parameter struct {
	Name        string  `json:"name"`
	In          string  `json:"in"`
	Required    bool    `json:"required"`
	Description string  `json:"description"`
	Schema      *schema `json:"schema"`
}

type schema struct {
	Ref                  string             `json:"$ref"`
	Type                 string             `json:"type"`
	Format               string             `json:"format"`
	Description          string             `json:"description"`
	Title                string             `json:"title"`
	Enum                 []string           `json:"enum"`
	Properties           map[string]*schema `json:"properties"`
	Items                *schema            `json:"items"`
	AdditionalProperties json.RawMessage    `json:"additionalProperties"`
	ReadOnly             bool               `json:"readOnly"`
}

func (s *schema) mapValue() *schema {
	if len(s.AdditionalProperties) == 0 || string(s.AdditionalProperties) == "false" {
		return nil
	}
	var v schema
	if string(s.AdditionalProperties) == "true" || json.Unmarshal(s.AdditionalProperties, &v) != nil {
		return &schema{}
	}
	return &v
}

func refName(ref string) string { return ref[strings.LastIndex(ref, "/")+1:] }

// ---------------------------------------------------------------- naming

var initialisms = map[string]string{
	"acl": "ACL", "api": "API", "asn": "ASN", "cidr": "CIDR", "cpu": "CPU", "cpus": "CPUs",
	"ddos": "DDoS", "dns": "DNS", "dnssec": "DNSSEC", "ds": "DS", "eula": "EULA", "fqdn": "FQDN",
	"gpu": "GPU", "html": "HTML", "http": "HTTP", "https": "HTTPS", "id": "ID", "ids": "IDs",
	"ip": "IP", "ipam": "IPAM", "ips": "IPs", "json": "JSON", "jwt": "JWT", "kyc": "KYC",
	"mfa": "MFA", "nat": "NAT", "os": "OS", "pdf": "PDF", "ptr": "PTR", "rdns": "RDNS",
	"sla": "SLA", "sctp": "SCTP", "icmp": "ICMP", "icmpv6": "ICMPv6", "sku": "SKU", "smtp": "SMTP", "sql": "SQL", "ssh": "SSH", "sso": "SSO",
	"ssl": "SSL", "tcp": "TCP", "tls": "TLS", "totp": "TOTP", "ttl": "TTL", "udp": "UDP",
	"ui": "UI", "uri": "URI", "url": "URL", "urls": "URLs", "utc": "UTC", "uuid": "UUID",
	"vat": "VAT", "vm": "VM", "vms": "VMs", "vnc": "VNC", "vps": "VPS", "xml": "XML",
}

// words splits camelCase, PascalCase, snake_case and kebab-case into words.
func words(s string) []string {
	var out []string
	var cur []rune
	flush := func() {
		if len(cur) > 0 {
			out = append(out, string(cur))
			cur = nil
		}
	}
	rs := []rune(s)
	for i, r := range rs {
		switch {
		case !unicode.IsLetter(r) && !unicode.IsDigit(r):
			flush()
		case unicode.IsUpper(r):
			// New word at an upper-case letter, unless inside an acronym that
			// is not followed by a lower-case letter.
			if len(cur) > 0 {
				prev := rs[i-1]
				nextLower := i+1 < len(rs) && unicode.IsLower(rs[i+1])
				if unicode.IsLower(prev) || unicode.IsDigit(prev) || (unicode.IsUpper(prev) && nextLower) {
					flush()
				}
			}
			cur = append(cur, r)
		case unicode.IsDigit(r):
			if len(cur) > 0 && !unicode.IsDigit(cur[len(cur)-1]) {
				// "ipv4", "oauth2", "k3s" keep their digit.
				w := strings.ToLower(string(cur))
				if w != "ipv" && w != "oauth" && w != "k" && w != "s" {
					flush()
				}
			}
			cur = append(cur, r)
		default:
			if len(cur) > 0 && unicode.IsDigit(cur[len(cur)-1]) {
				w := strings.ToLower(string(cur))
				if w != "k3" && !strings.HasPrefix(w, "ipv") {
					flush()
				}
			}
			cur = append(cur, r)
		}
	}
	flush()
	return out
}

// goName turns any API name into an exported Go identifier.
func goName(s string) string {
	var b strings.Builder
	for _, w := range words(s) {
		lw := strings.ToLower(w)
		switch {
		case initialisms[lw] != "":
			b.WriteString(initialisms[lw])
		case strings.HasPrefix(lw, "ipv"):
			b.WriteString("IPv" + w[3:])
		case lw == "oauth2" || lw == "oauth":
			b.WriteString(strings.Replace(lw, "oauth", "OAuth", 1))
		default:
			b.WriteString(strings.ToUpper(w[:1]) + strings.ToLower(w[1:]))
		}
	}
	name := b.String()
	if name == "" {
		return "X"
	}
	if unicode.IsDigit(rune(name[0])) {
		name = "N" + name
	}
	return name
}

var goKeywords = map[string]bool{
	"break": true, "case": true, "chan": true, "const": true, "continue": true, "default": true,
	"defer": true, "else": true, "fallthrough": true, "for": true, "func": true, "go": true,
	"goto": true, "if": true, "import": true, "interface": true, "map": true, "package": true,
	"range": true, "return": true, "select": true, "struct": true, "switch": true, "type": true,
	"var": true, "ctx": true, "params": true, "body": true, "s": true, "err": true,
}

// argName turns a path parameter into an unexported Go identifier.
func argName(s string) string {
	ws := words(s)
	if len(ws) == 0 {
		return "arg"
	}
	n := strings.ToLower(ws[0])
	for _, w := range ws[1:] {
		n += goName(w)
	}
	if goKeywords[n] {
		n += "Arg"
	}
	return n
}

// ---------------------------------------------------------------- generator

type generator struct {
	opsTable   bytes.Buffer
	spec       *spec
	typeNames  map[string]string // schema name -> Go type name
	used       map[string]bool
	bodySchema map[string]bool // schemas reachable from a request body
	models     bytes.Buffer
	services   bytes.Buffer
}

var serviceBody = regexp.MustCompile(`^(\w+?)Service(\w+Body)$`)

func (g *generator) nameTypes() {
	g.typeNames = map[string]string{}
	g.used = map[string]bool{"Client": true, "Error": true, "Option": true}
	names := sortedKeys(g.spec.Components.Schemas)
	// Pass 1: short names; pass 2: fall back to the full name on collision.
	want := map[string][]string{}
	for _, n := range names {
		short := n
		short = strings.TrimPrefix(short, "v1")
		if m := serviceBody.FindStringSubmatch(short); m != nil {
			short = m[2]
		}
		switch n {
		case "rpcStatus":
			short = "RPCStatus"
		case "protobufAny":
			short = "ProtobufAny"
		case "protobufNullValue":
			short = "ProtobufNullValue"
		}
		want[goName(short)] = append(want[goName(short)], n)
	}
	for _, n := range names {
		short := strings.TrimPrefix(n, "v1")
		if m := serviceBody.FindStringSubmatch(short); m != nil {
			short = m[2]
		}
		switch n {
		case "rpcStatus":
			short = "RPCStatus"
		case "protobufAny":
			short = "ProtobufAny"
		case "protobufNullValue":
			short = "ProtobufNullValue"
		}
		gn := goName(short)
		if len(want[gn]) > 1 || g.used[gn] {
			// On a clash, prefix the API area: BackupRestoreBackupBody.
			if m := serviceBody.FindStringSubmatch(strings.TrimPrefix(n, "v1")); m != nil {
				gn = goName(m[1] + m[2])
			} else {
				gn = goName(n)
			}
		}
		for g.used[gn] {
			gn += "Type"
		}
		g.used[gn] = true
		g.typeNames[n] = gn
	}
}

// markBodySchemas records every schema reachable from a request body, so
// their booleans can be pointers (false must be sendable in updates).
func (g *generator) markBodySchemas() {
	g.bodySchema = map[string]bool{}
	var visit func(s *schema)
	visit = func(s *schema) {
		if s == nil {
			return
		}
		if s.Ref != "" {
			n := refName(s.Ref)
			if g.bodySchema[n] {
				return
			}
			g.bodySchema[n] = true
			visit(g.spec.Components.Schemas[n])
			return
		}
		for _, p := range s.Properties {
			visit(p)
		}
		visit(s.Items)
		visit(s.mapValue())
	}
	for _, path := range g.spec.Paths {
		for _, op := range path {
			if s := g.requestSchema(op); s != nil {
				visit(s)
			}
		}
	}
}

func (g *generator) requestSchema(op *operation) *schema {
	if op.RequestBody == nil {
		return nil
	}
	if op.RequestBody.Ref != "" {
		rb, ok := g.spec.Components.RequestBodies[refName(op.RequestBody.Ref)]
		if !ok {
			return nil
		}
		return rb.Content["application/json"].Schema
	}
	return op.RequestBody.Content["application/json"].Schema
}

// enumByValues finds the named enum with exactly these values, so inline
// enums (for example in query parameters) get the typed constants too.
func (g *generator) enumByValues(vals []string) string {
	key := strings.Join(vals, "|")
	for _, n := range sortedKeys(g.spec.Components.Schemas) {
		if s := g.spec.Components.Schemas[n]; isEnum(s) && strings.Join(s.Enum, "|") == key {
			return g.typeNames[n]
		}
	}
	return ""
}

func isEnum(s *schema) bool { return s != nil && len(s.Enum) > 0 && s.Type == "string" }

// goType returns the Go type of a schema used as a field. inBody means the
// field belongs to a request body.
func (g *generator) goType(s *schema, inBody bool) (typ string, stringTag bool) {
	if s == nil {
		return "any", false
	}
	if s.Ref != "" {
		n := refName(s.Ref)
		target := g.spec.Components.Schemas[n]
		if n == "protobufAny" {
			return "map[string]any", false
		}
		if isEnum(target) {
			return g.typeNames[n], false
		}
		if target != nil && target.Type != "object" && target.Type != "" && len(target.Properties) == 0 {
			return g.goType(target, inBody)
		}
		return "*" + g.typeNames[n], false
	}
	if s.Type == "string" && len(s.Enum) > 0 {
		if n := g.enumByValues(s.Enum); n != "" {
			return n, false
		}
	}
	switch s.Type {
	case "string":
		switch s.Format {
		case "int64", "uint64":
			return "int64", true
		case "date-time":
			return "*time.Time", false
		case "byte":
			return "[]byte", false
		}
		return "string", false
	case "integer":
		if s.Format == "int64" {
			return "int64", false
		}
		return "int32", false
	case "number":
		if s.Format == "float" {
			return "float32", false
		}
		return "float64", false
	case "boolean":
		if inBody {
			return "*bool", false
		}
		return "bool", false
	case "array":
		elem, _ := g.goType(s.Items, inBody)
		if s.Items != nil && s.Items.Format == "int64" && s.Items.Type == "string" {
			return "Int64s", false
		}
		return "[]" + strings.TrimPrefix(elem, "*"), false
	case "object", "":
		if v := s.mapValue(); v != nil {
			elem, _ := g.goType(v, inBody)
			if v.Type == "" && v.Ref == "" {
				elem = "any"
			}
			return "map[string]" + strings.TrimPrefix(elem, "*"), false
		}
		return "map[string]any", false
	}
	return "any", false
}

func comment(buf *bytes.Buffer, indent, text string) {
	text = strings.TrimSpace(text)
	if text == "" {
		return
	}
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimRight(line, " \t")
		if line == "" {
			buf.WriteString(indent + "//\n")
			continue
		}
		buf.WriteString(indent + "// " + line + "\n")
	}
}

func (g *generator) genModels() {
	w := &g.models
	w.WriteString(header + "package noderings\n\nimport \"time\"\n\nvar _ time.Time\n\n")
	for _, n := range sortedKeys(g.spec.Components.Schemas) {
		s := g.spec.Components.Schemas[n]
		name := g.typeNames[n]
		if n == "protobufAny" {
			continue
		}
		if isEnum(s) {
			g.genEnum(name, s)
			continue
		}
		if s.Type != "object" && s.Type != "" {
			continue // scalar aliases are inlined where used
		}
		desc := s.Description
		if desc == "" {
			desc = s.Title
		}
		if desc != "" {
			comment(w, "", name+": "+desc)
		} else {
			fmt.Fprintf(w, "// %s is the API's %s object.\n", name, n)
		}
		fmt.Fprintf(w, "type %s struct {\n", name)
		inBody := g.bodySchema[n]
		fieldNames := map[string]bool{}
		for _, pn := range sortedKeys(s.Properties) {
			p := s.Properties[pn]
			fn := goName(pn)
			for fieldNames[fn] {
				fn += "_"
			}
			fieldNames[fn] = true
			typ, str := g.goType(p, inBody)
			tag := pn + ",omitempty"
			if str {
				tag += ",string"
			}
			pdesc := p.Description
			if pdesc == "" && p.Ref != "" {
				if t := g.spec.Components.Schemas[refName(p.Ref)]; t != nil {
					pdesc = t.Description
				}
			}
			if p.ReadOnly {
				pdesc = strings.TrimSpace(pdesc + "\n\nRead-only: set by the API.")
			}
			comment(w, "\t", pdesc)
			fmt.Fprintf(w, "\t%s %s `json:%q`\n", fn, typ, tag)
		}
		w.WriteString("}\n\n")
	}
}

func (g *generator) genEnum(name string, s *schema) {
	w := &g.models
	if s.Description != "" {
		comment(w, "", name+": "+s.Description)
	} else {
		fmt.Fprintf(w, "// %s is an API enum.\n", name)
	}
	fmt.Fprintf(w, "type %s string\n\n", name)
	prefix := commonPrefix(s.Enum)
	fmt.Fprintf(w, "// Values of %s.\nconst (\n", name)
	seen := map[string]bool{}
	for _, v := range s.Enum {
		c := name + goName(strings.TrimPrefix(v, prefix))
		for seen[c] {
			c += "_"
		}
		seen[c] = true
		fmt.Fprintf(w, "\t%s %s = %q\n", c, name, v)
	}
	w.WriteString(")\n\n")
}

// commonPrefix is the longest shared prefix ending in "_", for example
// "DISTRO_" for DISTRO_UBUNTU_24_04 and DISTRO_DEBIAN_12.
func commonPrefix(vals []string) string {
	if len(vals) < 2 {
		if len(vals) == 1 {
			if i := strings.LastIndex(vals[0], "_"); i > 0 {
				return vals[0][:i+1]
			}
		}
		return ""
	}
	p := vals[0]
	for _, v := range vals[1:] {
		for !strings.HasPrefix(v, p) {
			p = p[:len(p)-1]
		}
	}
	if i := strings.LastIndex(p, "_"); i >= 0 {
		p = p[:i+1]
	} else {
		p = ""
	}
	// Do not strip a prefix that would leave an empty or numeric-only name.
	for _, v := range vals {
		rest := strings.TrimPrefix(v, p)
		if rest == "" || unicode.IsDigit(rune(rest[0])) {
			return ""
		}
	}
	return p
}

type opInfo struct {
	method, path, httpMethod string
	op                       *operation
	pathParams               []*parameter
	queryParams              []*parameter
}

func (g *generator) genServices() []string {
	byTag := map[string][]opInfo{}
	for path, ops := range g.spec.Paths {
		for m, op := range ops {
			switch m {
			case "get", "post", "put", "patch", "delete":
			default:
				continue
			}
			tag := "DefaultService"
			if len(op.Tags) > 0 {
				tag = op.Tags[0]
			}
			if excludedTags[tag] {
				continue
			}
			oi := opInfo{path: path, httpMethod: strings.ToUpper(m), op: op}
			oi.method = goName(op.OperationID[strings.Index(op.OperationID, "_")+1:])
			// Path parameters in the order they appear in the path.
			for _, seg := range regexp.MustCompile(`\{([^}]+)\}`).FindAllStringSubmatch(path, -1) {
				for _, p := range op.Parameters {
					if p.In == "path" && p.Name == seg[1] {
						oi.pathParams = append(oi.pathParams, p)
					}
				}
			}
			for _, p := range op.Parameters {
				if p.In == "query" {
					oi.queryParams = append(oi.queryParams, p)
				}
			}
			byTag[tag] = append(byTag[tag], oi)
		}
	}
	w := &g.services
	w.WriteString(header + `package noderings

import (
	"context"
	"iter"
	"net/http"
	"net/url"
	"time"
)

var (
	_ = url.PathEscape
	_ time.Time
	_ iter.Seq2[int, error]
)

`)
	var tags []string
	for t := range byTag {
		tags = append(tags, t)
	}
	sort.Strings(tags)
	var fields []string
	for _, tag := range tags {
		ops := byTag[tag]
		sort.Slice(ops, func(i, j int) bool { return ops[i].method < ops[j].method })
		svc := goName(strings.TrimSuffix(tag, "Service")) + "Service"
		field := strings.TrimSuffix(svc, "Service")
		fields = append(fields, field+"|"+svc+"|"+tag)
		fmt.Fprintf(w, "// %s calls the NodeRings %s API.\ntype %s struct{ client *Client }\n\n", svc, tag, svc)
		for _, oi := range ops {
			g.genOperation(svc, oi)
		}
	}
	// Client wiring.
	w.WriteString("// services are the API areas of a Client.\ntype services struct {\n")
	for _, f := range fields {
		p := strings.Split(f, "|")
		fmt.Fprintf(w, "\t// %s calls the %s API.\n\t%s *%s\n", p[0], p[2], p[0], p[1])
	}
	w.WriteString("}\n\nfunc (c *Client) initServices() {\n")
	for _, f := range fields {
		p := strings.Split(f, "|")
		fmt.Fprintf(w, "\tc.%s = &%s{client: c}\n", p[0], p[1])
	}
	w.WriteString("}\n")
	return fields
}

func (g *generator) genOperation(svc string, oi opInfo) {
	w := &g.services
	op := oi.op
	fmt.Fprintf(&g.opsTable, "\t{%q, %q, %q},\n", strings.TrimSuffix(svc, "Service"), oi.method, op.OperationID)
	// Response type.
	resp := "SuccessResponse"
	if r, ok := op.Responses["200"]; ok {
		if s := r.Content["application/json"].Schema; s != nil && s.Ref != "" {
			resp = g.typeNames[refName(s.Ref)]
		}
	}
	// Body type.
	bodyType := ""
	bodySchemaName := ""
	emptyBody := false
	if s := g.requestSchema(op); s != nil {
		if s.Ref != "" {
			bodySchemaName = refName(s.Ref)
			bodyType = g.typeNames[bodySchemaName]
		} else {
			// An inline body with no fields: the API wants {}.
			emptyBody = true
		}
	}
	// Query parameter struct.
	paramsType := ""
	if len(oi.queryParams) > 0 {
		paramsType = oi.method + "Params"
		for g.used[paramsType] {
			paramsType = strings.TrimSuffix(svc, "Service") + paramsType
		}
		g.used[paramsType] = true
		fmt.Fprintf(w, "// %s are the query parameters of %s.%s.\ntype %s struct {\n", paramsType, svc, oi.method, paramsType)
		for _, p := range oi.queryParams {
			typ, _ := g.goType(p.Schema, true)
			comment(w, "\t", p.Description)
			fmt.Fprintf(w, "\t%s %s\n", goName(p.Name), typ)
		}
		w.WriteString("}\n\n")
		fmt.Fprintf(w, "func (p *%s) values() url.Values {\n\tq := url.Values{}\n\tif p == nil {\n\t\treturn q\n\t}\n", paramsType)
		for _, p := range oi.queryParams {
			fmt.Fprintf(w, "\taddQuery(q, %q, p.%s)\n", p.Name, goName(p.Name))
		}
		w.WriteString("\treturn q\n}\n\n")
	}

	// Signature.
	var args []string
	args = append(args, "ctx context.Context")
	pathExpr := fmt.Sprintf("%q", oi.path)
	for _, p := range oi.pathParams {
		a := argName(p.Name)
		args = append(args, a+" string")
		pathExpr = strings.Replace(pathExpr, "{"+p.Name+"}", `"+url.PathEscape(`+a+`)+"`, 1)
	}
	pathExpr = strings.ReplaceAll(pathExpr, `+""`, "")
	pathExpr = strings.TrimPrefix(pathExpr, `""+`)
	if paramsType != "" {
		args = append(args, "params *"+paramsType)
	}
	if bodyType != "" {
		args = append(args, "body *"+bodyType)
	}
	doc := strings.TrimSpace(op.Summary)
	if op.Description != "" && op.Description != op.Summary {
		doc = strings.TrimSpace(doc + "\n\n" + op.Description)
	}
	if doc == "" {
		comment(w, "", fmt.Sprintf("%s calls %s %s.", oi.method, oi.httpMethod, oi.path))
	} else {
		comment(w, "", oi.method+": "+doc+"\n\nIt calls "+oi.httpMethod+" "+oi.path+".")
	}
	idem := bodySchemaName != "" && hasProp(g.spec.Components.Schemas[bodySchemaName], "idempotencyKey")
	if idem {
		comment(w, "", "If body.IdempotencyKey is empty, a new UUID is set on body before the request,\nso a retry of the same body cannot create a second resource.")
	}
	fmt.Fprintf(w, "func (s *%s) %s(%s) (*%s, error) {\n", svc, oi.method, strings.Join(args, ", "), resp)
	if idem {
		w.WriteString("\tif body != nil && body.IdempotencyKey == \"\" {\n\t\tbody.IdempotencyKey = NewIdempotencyKey()\n\t}\n")
	}
	query := "nil"
	if paramsType != "" {
		query = "params.values()"
	}
	bodyArg := "nil"
	if bodyType != "" {
		bodyArg = "body"
	}
	fmt.Fprintf(w, "\tout := new(%s)\n", resp)
	fmt.Fprintf(w, "\terr := s.client.call(ctx, &request{method: http.Method%s, path: %s, query: %s, body: %s, idempotent: %v}, out)\n",
		httpMethodName(oi.httpMethod), pathExpr, query, bodyArgExpr(bodyArg, bodyType, emptyBody), idem)
	w.WriteString("\tif err != nil {\n\t\treturn nil, err\n\t}\n\treturn out, nil\n}\n\n")

	g.genIterator(svc, oi, paramsType, resp, args)
}

// httpMethodName turns "GET" into "Get", for http.MethodGet.
func httpMethodName(m string) string { return m[:1] + strings.ToLower(m[1:]) }

func bodyArgExpr(arg, typ string, empty bool) string {
	if empty {
		return "struct{}{}"
	}
	if typ == "" {
		return "nil"
	}
	return "bodyOrNil(" + arg + ")"
}

func hasProp(s *schema, name string) bool {
	if s == nil {
		return false
	}
	_, ok := s.Properties[name]
	return ok
}

// genIterator adds MethodAll for list operations that take page and pageSize
// and return one array.
func (g *generator) genIterator(svc string, oi opInfo, paramsType, resp string, args []string) {
	if paramsType == "" {
		return
	}
	var hasPage, hasSize bool
	for _, p := range oi.queryParams {
		hasPage = hasPage || p.Name == "page"
		hasSize = hasSize || p.Name == "pageSize"
	}
	if !hasPage || !hasSize {
		return
	}
	var respSchema *schema
	for n, gn := range g.typeNames {
		if gn == resp {
			respSchema = g.spec.Components.Schemas[n]
		}
	}
	if respSchema == nil {
		return
	}
	// The paged array: the only array, or else the only array of objects
	// (ListDNSRecords also returns the list of record types in use).
	var arrays, objectArrays []string
	for _, pn := range sortedKeys(respSchema.Properties) {
		if p := respSchema.Properties[pn]; p.Type == "array" {
			arrays = append(arrays, pn)
			if p.Items != nil && p.Items.Ref != "" {
				objectArrays = append(objectArrays, pn)
			}
		}
	}
	if len(arrays) != 1 {
		arrays = objectArrays
	}
	if len(arrays) != 1 {
		return
	}
	arr := respSchema.Properties[arrays[0]]
	elem, _ := g.goType(arr.Items, false)
	elem = strings.TrimPrefix(elem, "*")
	_, hasTotal := respSchema.Properties["totalCount"]
	var callArgs []string
	callArgs = append(callArgs, "ctx")
	for _, a := range args[1:] {
		name := strings.Fields(a)[0]
		if name == "params" {
			callArgs = append(callArgs, "&p")
		} else {
			callArgs = append(callArgs, name)
		}
	}
	w := &g.services
	comment(w, "", fmt.Sprintf("%sAll returns an iterator over every result of %s, reading\npage after page. params.Page is the first page (0 by default) and\nparams.PageSize the page size (%d by default).", oi.method, oi.method, 100))
	fmt.Fprintf(w, "func (s *%s) %sAll(%s) iter.Seq2[%s, error] {\n", svc, oi.method, strings.Join(args, ", "), elem)
	w.WriteString("\treturn func(yield func(" + elem + ", error) bool) {\n")
	fmt.Fprintf(w, "\t\tvar p %s\n\t\tif params != nil {\n\t\t\tp = *params\n\t\t}\n\t\tif p.PageSize <= 0 {\n\t\t\tp.PageSize = defaultPageSize\n\t\t}\n\t\tseen := 0\n", paramsType)
	w.WriteString("\t\tfor {\n")
	fmt.Fprintf(w, "\t\t\tpage, err := s.%s(%s)\n", oi.method, strings.Join(callArgs, ", "))
	fmt.Fprintf(w, "\t\t\tif err != nil {\n\t\t\t\tvar zero %s\n\t\t\t\tyield(zero, err)\n\t\t\t\treturn\n\t\t\t}\n", elem)
	f := goName(arrays[0])
	fmt.Fprintf(w, "\t\t\tfor _, item := range page.%s {\n\t\t\t\tif !yield(item, nil) {\n\t\t\t\t\treturn\n\t\t\t\t}\n\t\t\t}\n", f)
	fmt.Fprintf(w, "\t\t\tseen += len(page.%s)\n", f)
	if hasTotal {
		fmt.Fprintf(w, "\t\t\tif len(page.%s) < int(p.PageSize) || (page.TotalCount > 0 && seen >= int(page.TotalCount)) {\n\t\t\t\treturn\n\t\t\t}\n", f)
	} else {
		fmt.Fprintf(w, "\t\t\tif len(page.%s) < int(p.PageSize) {\n\t\t\t\treturn\n\t\t\t}\n", f)
	}
	w.WriteString("\t\t\tp.Page++\n\t\t}\n\t}\n}\n\n")
}

const header = "// Code generated by internal/gen from api/openapi.json. DO NOT EDIT.\n\n"

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func writeGo(path string, src []byte) {
	out, err := format.Source(src)
	if err != nil {
		_ = os.WriteFile(path+".broken", src, 0o644)
		log.Fatalf("formatting %s: %v (unformatted source in %s.broken)", path, err, path)
	}
	if err := os.WriteFile(path, out, 0o644); err != nil {
		log.Fatal(err)
	}
}

func main() {
	in := flag.String("spec", "api/openapi.json", "OpenAPI description")
	out := flag.String("out", ".", "output directory")
	flag.Parse()
	raw, err := os.ReadFile(*in)
	if err != nil {
		log.Fatal(err)
	}
	var sp spec
	if err := json.Unmarshal(raw, &sp); err != nil {
		log.Fatal(err)
	}
	g := &generator{spec: &sp}
	g.nameTypes()
	g.markBodySchemas()
	g.genModels()
	fields := g.genServices()
	writeGo(filepath.Join(*out, "zz_generated_models.go"), g.models.Bytes())
	writeGo(filepath.Join(*out, "zz_generated_services.go"), g.services.Bytes())
	var t bytes.Buffer
	t.WriteString(header + "package noderings\n\n// generatedOperations maps each generated method to its API operation, for tests.\nvar generatedOperations = []struct{ service, method, operationID string }{\n")
	t.Write(g.opsTable.Bytes())
	t.WriteString("}\n")
	writeGo(filepath.Join(*out, "zz_generated_operations_test.go"), t.Bytes())
	log.Printf("generated %d services and %d types", len(fields), len(g.typeNames))
}
