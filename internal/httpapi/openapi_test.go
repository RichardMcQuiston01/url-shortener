package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/richardmcquiston01/url-shortener/internal/links"
	"github.com/richardmcquiston01/url-shortener/internal/store"
)

// The contract tests keep openapi.json honest: the spec is read from the repo
// root and compared with what the real handler returns, so the two cannot drift
// apart without a test failing.

const specPath string = "../../openapi.json"

var allErrorCodes []ErrorCode = []ErrorCode{
	CodeUnauthorized, CodeInvalidRequest, CodeInvalidURL, CodeInvalidSlug, CodeSlugReserved,
	CodeSlugTaken, CodeNotFound, CodeRateLimited, CodeInternalError,
}

type object = map[string]any

func loadSpec(t *testing.T) object {
	t.Helper()
	raw, err := os.ReadFile(specPath)
	if err != nil {
		t.Fatalf("read %s: %v", specPath, err)
	}
	var spec object
	if err := json.Unmarshal(raw, &spec); err != nil {
		t.Fatalf("%s is not valid JSON: %v", specPath, err)
	}
	return spec
}

// resolve follows "#/a/b/c" references inside the spec.
func resolve(t *testing.T, spec object, node any) any {
	t.Helper()
	for {
		asObject, isObject := node.(object)
		if !isObject {
			return node
		}
		reference, hasReference := asObject["$ref"].(string)
		if !hasReference {
			return node
		}
		if !strings.HasPrefix(reference, "#/") {
			t.Fatalf("only local references are supported, got %q", reference)
		}
		node = spec
		for _, part := range strings.Split(strings.TrimPrefix(reference, "#/"), "/") {
			next, found := node.(object)[part]
			if !found {
				t.Fatalf("reference %q does not resolve (missing %q)", reference, part)
			}
			node = next
		}
	}
}

func objectAt(t *testing.T, node any, description string) object {
	t.Helper()
	asObject, ok := node.(object)
	if !ok {
		t.Fatalf("%s should be an object, got %T", description, node)
	}
	return asObject
}

// validate checks value against a (small) subset of JSON Schema: $ref, allOf,
// type (including type lists), enum, const, required, properties,
// additionalProperties:false, items, pattern, minimum, maximum and the length
// limits. That is every keyword the spec uses.
func validate(t *testing.T, spec object, schemaNode any, value any, location string) {
	t.Helper()
	schema := objectAt(t, resolve(t, spec, schemaNode), location+" schema")

	if parts, ok := schema["allOf"].([]any); ok {
		for _, part := range parts {
			validate(t, spec, part, value, location)
		}
	}
	if allowed, ok := typeNames(schema["type"]); ok && !matchesAnyType(allowed, value) {
		t.Errorf("%s: %v is not of type %v", location, value, allowed)
		return
	}
	if constant, ok := schema["const"]; ok && value != constant {
		t.Errorf("%s: %v should be the constant %v", location, value, constant)
	}
	if enum, ok := schema["enum"].([]any); ok && !contains(enum, value) {
		t.Errorf("%s: %v is not one of %v", location, value, enum)
	}

	switch typed := value.(type) {
	case string:
		if pattern, ok := schema["pattern"].(string); ok && !regexp.MustCompile(pattern).MatchString(typed) {
			t.Errorf("%s: %q does not match %s", location, typed, pattern)
		}
		if limit, ok := schema["maxLength"].(float64); ok && float64(len([]rune(typed))) > limit {
			t.Errorf("%s: %q is longer than %v", location, typed, limit)
		}
	case float64:
		if minimum, ok := schema["minimum"].(float64); ok && typed < minimum {
			t.Errorf("%s: %v is below the minimum %v", location, typed, minimum)
		}
		if maximum, ok := schema["maximum"].(float64); ok && typed > maximum {
			t.Errorf("%s: %v is above the maximum %v", location, typed, maximum)
		}
	case []any:
		if items, ok := schema["items"]; ok {
			for index, element := range typed {
				validate(t, spec, items, element, fmt.Sprintf("%s[%d]", location, index))
			}
		}
	case object:
		properties, _ := schema["properties"].(object)
		if required, ok := schema["required"].([]any); ok {
			for _, name := range required {
				if _, present := typed[name.(string)]; !present {
					t.Errorf("%s: required field %q is missing", location, name)
				}
			}
		}
		for name, child := range typed {
			if propertySchema, known := properties[name]; known {
				validate(t, spec, propertySchema, child, location+"."+name)
			} else if schema["additionalProperties"] == false {
				t.Errorf("%s: field %q is not in the schema", location, name)
			}
		}
	}
}

func typeNames(node any) ([]string, bool) {
	switch typed := node.(type) {
	case string:
		return []string{typed}, true
	case []any:
		names := make([]string, 0, len(typed))
		for _, name := range typed {
			names = append(names, name.(string))
		}
		return names, true
	}
	return nil, false
}

func matchesAnyType(allowed []string, value any) bool {
	for _, name := range allowed {
		switch name {
		case "null":
			if value == nil {
				return true
			}
		case "string":
			if _, ok := value.(string); ok {
				return true
			}
		case "boolean":
			if _, ok := value.(bool); ok {
				return true
			}
		case "integer":
			if number, ok := value.(float64); ok && number == float64(int64(number)) {
				return true
			}
		case "number":
			if _, ok := value.(float64); ok {
				return true
			}
		case "array":
			if _, ok := value.([]any); ok {
				return true
			}
		case "object":
			if _, ok := value.(object); ok {
				return true
			}
		}
	}
	return false
}

func contains(list []any, value any) bool {
	for _, element := range list {
		if element == value {
			return true
		}
	}
	return false
}

func operationsOf(t *testing.T, spec object) map[string]object {
	t.Helper()
	operations := map[string]object{}
	for path, pathItem := range objectAt(t, spec["paths"], "paths") {
		for method, operation := range objectAt(t, pathItem, path) {
			operations[strings.ToUpper(method)+" "+path] = objectAt(t, operation, method+" "+path)
		}
	}
	return operations
}

func TestOpenAPIDescribesExactlyTheAPIRoutes(t *testing.T) {
	spec := loadSpec(t)
	if version, _ := spec["openapi"].(string); !strings.HasPrefix(version, "3.1") {
		t.Errorf("openapi = %q, want 3.1.x", version)
	}

	expected := map[string]string{
		"POST /api/links":       "createLink",
		"GET /api/links":        "listLinks",
		"GET /api/links/{slug}": "getLink",
	}
	operations := operationsOf(t, spec)
	if len(operations) != len(expected) {
		t.Errorf("the spec documents %d operations, want %d", len(operations), len(expected))
	}
	for route, operationID := range expected {
		operation, found := operations[route]
		if !found {
			t.Errorf("%s is not documented", route)
			continue
		}
		if operation["operationId"] != operationID {
			t.Errorf("%s operationId = %v, want %s", route, operation["operationId"], operationID)
		}
	}

	security, _ := spec["security"].([]any)
	if len(security) != 1 || objectAt(t, security[0], "security")["bearerAuth"] == nil {
		t.Error("every operation should require the bearerAuth scheme through the top-level security")
	}
	schemes := objectAt(t, objectAt(t, spec["components"], "components")["securitySchemes"], "securitySchemes")
	bearer := objectAt(t, schemes["bearerAuth"], "bearerAuth")
	if bearer["type"] != "http" || bearer["scheme"] != "bearer" {
		t.Errorf("bearerAuth = %v, want an http bearer scheme", bearer)
	}
}

func TestOpenAPIErrorCodesMatchTheGoCodes(t *testing.T) {
	spec := loadSpec(t)
	schemas := objectAt(t, objectAt(t, spec["components"], "components")["schemas"], "schemas")
	enum := objectAt(t, schemas["ErrorCode"], "ErrorCode")["enum"].([]any)

	documented := make([]string, 0, len(enum))
	for _, code := range enum {
		documented = append(documented, code.(string))
	}
	actual := make([]string, 0, len(allErrorCodes))
	for _, code := range allErrorCodes {
		actual = append(actual, string(code))
	}
	sort.Strings(documented)
	sort.Strings(actual)
	if strings.Join(documented, ",") != strings.Join(actual, ",") {
		t.Errorf("ErrorCode enum = %v, Go codes = %v", documented, actual)
	}
}

func TestOpenAPILimitsMatchTheGoLimits(t *testing.T) {
	spec := loadSpec(t)
	schemas := objectAt(t, objectAt(t, spec["components"], "components")["schemas"], "schemas")
	request := objectAt(t, objectAt(t, schemas["CreateLinkRequest"], "CreateLinkRequest")["properties"], "properties")

	if limit := objectAt(t, request["url"], "url")["maxLength"]; limit != float64(links.MaxTargetURLLength) {
		t.Errorf("url maxLength = %v, want %d", limit, links.MaxTargetURLLength)
	}
	if limit := objectAt(t, request["note"], "note")["maxLength"]; limit != float64(links.MaxNoteLength) {
		t.Errorf("note maxLength = %v, want %d", limit, links.MaxNoteLength)
	}
	wantPattern := fmt.Sprintf("^[A-Za-z0-9_-]{1,%d}$", links.MaxSlugLength)
	if pattern := objectAt(t, request["slug"], "slug")["pattern"]; pattern != wantPattern {
		t.Errorf("slug pattern = %v, want %s", pattern, wantPattern)
	}

	listParameters := operationsOf(t, spec)["GET /api/links"]["parameters"].([]any)
	for _, parameter := range listParameters {
		parameterObject := objectAt(t, parameter, "parameter")
		if parameterObject["name"] != "limit" {
			continue
		}
		schema := objectAt(t, parameterObject["schema"], "limit schema")
		if schema["maximum"] != float64(maxListLimit) || schema["default"] != float64(defaultListLimit) {
			t.Errorf("limit schema = %v, want maximum %d and default %d", schema, maxListLimit, defaultListLimit)
		}
	}
}

type contractCase struct {
	name          string
	method        string
	path          string // what is requested
	specPath      string // the same route as written in the spec
	body          string
	authorization string
	status        int
	brokenStore   bool
}

func TestOpenAPIMatchesTheRealResponses(t *testing.T) {
	spec := loadSpec(t)
	operations := operationsOf(t, spec)
	server := newTestServer(t, links.RandomSlugGenerator{})
	// Two servers whose stores fail, to trigger the documented 500 responses.
	brokenWrites := buildMux(t, brokenLookupStore{server.linkStore}, server.linkStore, links.RandomSlugGenerator{})
	brokenReads := buildMux(t, server.linkStore, brokenStatsStore{server.linkStore}, links.RandomSlugGenerator{})
	validKey := "Bearer " + testAPIKey

	cases := []contractCase{
		{"create with a custom slug", "POST", "/api/links", "/api/links", `{"url":"https://example.com/a","slug":"hello","note":"demo"}`, validKey, 201, false},
		{"create with a generated slug", "POST", "/api/links", "/api/links", `{"url":"https://example.com/gen"}`, validKey, 201, false},
		{"create again returns the existing link", "POST", "/api/links", "/api/links", `{"url":"https://example.com/gen"}`, validKey, 200, false},
		{"invalid url", "POST", "/api/links", "/api/links", `{"url":"ftp://example.com"}`, validKey, 400, false},
		{"invalid slug", "POST", "/api/links", "/api/links", `{"url":"https://example.com/b","slug":"a b"}`, validKey, 400, false},
		{"reserved slug", "POST", "/api/links", "/api/links", `{"url":"https://example.com/b","slug":"admin"}`, validKey, 400, false},
		{"malformed body", "POST", "/api/links", "/api/links", `{"url":`, validKey, 400, false},
		{"slug taken", "POST", "/api/links", "/api/links", `{"url":"https://example.com/c","slug":"hello"}`, validKey, 409, false},
		{"body too large", "POST", "/api/links", "/api/links", `{"url":"https://example.com/` + strings.Repeat("a", 17*1024) + `"}`, validKey, 413, false},
		{"missing key", "POST", "/api/links", "/api/links", `{"url":"https://example.com/d"}`, "", 401, false},
		{"internal error", "POST", "/api/links", "/api/links", `{"url":"https://example.com/e"}`, validKey, 500, true},
		{"list", "GET", "/api/links?limit=1", "/api/links", "", validKey, 200, false},
		{"list with a bad limit", "GET", "/api/links?limit=0", "/api/links", "", validKey, 400, false},
		{"list without a key", "GET", "/api/links", "/api/links", "", "", 401, false},
		{"list with an internal error", "GET", "/api/links", "/api/links", "", validKey, 500, true},
		{"get one with an internal error", "GET", "/api/links/hello", "/api/links/{slug}", "", validKey, 500, true},
		{"get one", "GET", "/api/links/hello", "/api/links/{slug}", "", validKey, 200, false},
		{"get an unknown slug", "GET", "/api/links/missing", "/api/links/{slug}", "", validKey, 404, false},
		{"get one without a key", "GET", "/api/links/hello", "/api/links/{slug}", "", "", 401, false},
	}

	exercised := map[string]bool{}
	seenCodes := map[string]bool{}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			mux := server.mux
			if testCase.brokenStore && testCase.method == "POST" {
				mux = brokenWrites
			} else if testCase.brokenStore {
				mux = brokenReads
			}
			recorder := doRequest(mux, testCase.method, testCase.path, testCase.body, testCase.authorization)
			if recorder.Code != testCase.status {
				t.Fatalf("status = %d, want %d: %s", recorder.Code, testCase.status, recorder.Body.String())
			}

			route := testCase.method + " " + testCase.specPath
			operation, documented := operations[route]
			if !documented {
				t.Fatalf("%s is not in the spec", route)
			}
			responses := objectAt(t, operation["responses"], route+" responses")
			statusKey := fmt.Sprint(recorder.Code)
			responseNode, found := responses[statusKey]
			if !found {
				t.Fatalf("%s answered %s, which the spec does not document", route, statusKey)
			}
			response := objectAt(t, resolve(t, spec, responseNode), route+" "+statusKey)
			exercised[route+" "+statusKey] = true

			if headers, ok := response["headers"].(object); ok {
				for name := range headers {
					if recorder.Header().Get(name) == "" {
						t.Errorf("the spec documents the %s header but it was not sent", name)
					}
				}
			}

			content := objectAt(t, response["content"], route+" "+statusKey+" content")
			schema := objectAt(t, content["application/json"], "application/json")["schema"]
			var body any
			if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
				t.Fatalf("response is not JSON: %v: %s", err, recorder.Body.String())
			}
			validate(t, spec, schema, body, route+" "+statusKey)

			if codes, ok := response["x-error-codes"].([]any); ok {
				actual := body.(object)["error"]
				if !contains(codes, actual) {
					t.Errorf("error code %v is not among the documented %v", actual, codes)
				}
				seenCodes[route+" "+statusKey+" "+fmt.Sprint(actual)] = true
			}
		})
	}

	// Everything the spec promises must be exercised at least once.
	for route, operation := range operations {
		for statusKey, responseNode := range objectAt(t, operation["responses"], route+" responses") {
			if !exercised[route+" "+statusKey] {
				t.Errorf("the spec documents %s %s but no contract case triggers it", route, statusKey)
			}
			response := objectAt(t, resolve(t, spec, responseNode), route+" "+statusKey)
			if codes, ok := response["x-error-codes"].([]any); ok {
				for _, code := range codes {
					if !seenCodes[route+" "+statusKey+" "+fmt.Sprint(code)] {
						t.Errorf("the spec says %s %s can return %v but no contract case produces it", route, statusKey, code)
					}
				}
			}
		}
	}
}

// brokenStatsStore fails every read, as a damaged database would.
type brokenStatsStore struct{ store.StatsStore }

func (brokenStatsStore) ListLinkSummaries(context.Context, int, int) ([]store.LinkSummary, error) {
	return nil, errors.New("disk on fire")
}

func (brokenStatsStore) GetLinkSummaryBySlug(context.Context, string) (store.LinkSummary, error) {
	return store.LinkSummary{}, errors.New("disk on fire")
}
