package domain

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

// TestContextReportCarriesOnlyCounts is a structural guard, not a
// behavioural one.
//
// The report is stored on a message and sent to the browser, so the moment
// somebody adds a `Content`, `Text`, `Title` or `Items []Memory` field to
// it, three things become true at once: every turn pays to duplicate text
// that is already stored, deleting a memory stops actually deleting it, and
// a panel meant to show counts starts showing content.
//
// Reflecting over the field types is the only way to state that in a test
// rather than in a comment nobody reads.
func TestContextReportCarriesOnlyCounts(t *testing.T) {
	allowed := map[string]bool{
		"string": true, // named string types, checked by name below
		"int":    true,
		// float64 arrived with tool rounds: a round records what that one
		// provider call cost, which is a number in dollars and cannot be an
		// integer. It is still a measurement, not content.
		"float64": true,
	}

	// The closed vocabularies the report is allowed to name. Every one of
	// them is a Go constant set with a fixed number of values, so none can
	// carry text somebody typed:
	//
	//	Kind         BlockKind         — the seven context blocks
	//	Reason       ExclusionReason   — the four ways something is left out
	//	UsageSource  UsageSource       — unknown | provider | estimated
	//	FinishReason string            — the provider's own terminal reasons
	//	Name         ToolName          — an identifier from the code registry
	//	Status       ToolCallStatus    — ok | error
	//	ErrorCode    ToolErrorCode     — the six tool failure codes
	//
	//	CacheBreakpointAfter BlockKind — which block this turn asked the
	//	                                 provider to cache up to
	//
	// The last one earns its place on the same terms as the others and not
	// as an exception: its type is BlockKind, the identical closed set the
	// Kind field already draws from, so it can only ever name one of the
	// blocks this package defines. Nothing a user typed can reach it.
	//
	// A field named Content, Text, Title, Arguments or Result still fails,
	// which is the whole point: the report says how much and why, and the
	// payloads live in chat.tool_calls, which is the record built to be
	// redactable.
	vocabulary := map[string]bool{
		"Kind": true, "Reason": true, "UsageSource": true, "FinishReason": true,
		"Name": true, "Status": true, "ErrorCode": true,
		"CacheBreakpointAfter": true,
	}

	var walk func(t reflect.Type, path string)
	walk = func(rt reflect.Type, path string) {
		for i := 0; i < rt.NumField(); i++ {
			f := rt.Field(i)
			ft := f.Type
			for ft.Kind() == reflect.Slice || ft.Kind() == reflect.Ptr {
				ft = ft.Elem()
			}
			where := path + "." + f.Name
			if ft.Kind() == reflect.Struct {
				walk(ft, where)
				continue
			}
			if !allowed[ft.Kind().String()] {
				t.Fatalf("%s is a %s; a context report may only carry counts and reasons",
					where, ft.Kind())
			}
			// A string field is a category, never free text.
			if ft.Kind() == reflect.String && !vocabulary[f.Name] {
				t.Fatalf("%s is a string field outside the closed vocabulary; "+
					"the report must not carry content", where)
			}
		}
	}
	walk(reflect.TypeOf(ContextReport{}), "ContextReport")
}

// TestDegraded reports the blocks whose source could not be read, and only
// those. Every surface answers "was anything missing?" from this one place.
func TestDegraded(t *testing.T) {
	cases := []struct {
		name   string
		report ContextReport
		want   []BlockKind
	}{
		{
			name:   "nothing degraded",
			report: ContextReport{Blocks: []ContextBlock{{Kind: BlockMemory, Items: 2}}},
		},
		{
			name: "a budget cut is not a degradation",
			report: ContextReport{Blocks: []ContextBlock{{
				Kind:       BlockSources,
				Exclusions: []ContextExclusion{{Reason: ReasonBudget, Items: 1}},
			}}},
		},
		{
			name: "an unreadable block is",
			report: ContextReport{Blocks: []ContextBlock{{
				Kind:       BlockMemory,
				Exclusions: []ContextExclusion{{Reason: ReasonUnavailable}},
			}}},
			want: []BlockKind{BlockMemory},
		},
		{
			name: "both, in composition order",
			report: ContextReport{Blocks: []ContextBlock{
				{Kind: BlockMemory, Exclusions: []ContextExclusion{{Reason: ReasonUnavailable}}},
				{Kind: BlockSources, Exclusions: []ContextExclusion{
					{Reason: ReasonBudget, Items: 1},
					{Reason: ReasonUnavailable},
				}},
			}},
			want: []BlockKind{BlockMemory, BlockSources},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := tc.report.Degraded()
			if len(got) != len(tc.want) {
				t.Fatalf("Degraded() = %v, want %v", got, tc.want)
			}
			for i := range tc.want {
				if got[i] != tc.want[i] {
					t.Fatalf("Degraded() = %v, want %v", got, tc.want)
				}
			}
		})
	}
}

// TestContextReportRoundTrip: the report is stored as a document and read
// back, so it has to survive the trip unchanged — including an empty
// exclusion list staying absent rather than becoming `null`.
func TestContextReportRoundTrip(t *testing.T) {
	in := ContextReport{
		Blocks: []ContextBlock{
			{Kind: BlockInstructions, Items: 1, Characters: 100, EstimatedTokens: 25},
			{Kind: BlockSources, Items: 2, Characters: 400, EstimatedTokens: 100, Exclusions: []ContextExclusion{
				{Reason: ReasonTooLarge, Items: 1, Characters: 20000},
			}},
		},
		TotalCharacters:      500,
		TotalEstimatedTokens: 125,
	}

	raw, err := json.Marshal(in)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if strings.Contains(string(raw), "exclusions\":null") {
		t.Fatalf("a block with no exclusions serialised a null: %s", raw)
	}

	var out ContextReport
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if !reflect.DeepEqual(in, out) {
		t.Fatalf("round trip changed the report:\n in  %+v\n out %+v", in, out)
	}
}

// TestBlockLookup: an absent kind is absent, not a zero-valued block. The
// difference is "this contributed nothing" versus "this was never asked".
func TestBlockLookup(t *testing.T) {
	r := ContextReport{Blocks: []ContextBlock{{Kind: BlockMemory, Items: 1}}}

	if b, ok := r.Block(BlockMemory); !ok || b.Items != 1 {
		t.Fatalf("Block(memory) = %+v, %v", b, ok)
	}
	if _, ok := r.Block(BlockSources); ok {
		t.Fatalf("Block(sources) reported a block that is not in the report")
	}
}
