package main

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestApplyTemplate_SingleVariable(t *testing.T) {
	got := ApplyTemplate("Hi {{name}}!", map[string]string{"name": "Alice"})
	assert.Equal(t, "Hi Alice!", got)
}

func TestApplyTemplate_MultipleVariables(t *testing.T) {
	got := ApplyTemplate("Hi {{name}}, your code is {{code}}",
		map[string]string{"name": "Alice", "code": "A123"})
	assert.Equal(t, "Hi Alice, your code is A123", got)
}

func TestApplyTemplate_MissingVariableStaysAsPlaceholder(t *testing.T) {
	// Unreplaced placeholders remain in the output (caller's problem to validate).
	got := ApplyTemplate("Hi {{name}}, your code is {{code}}",
		map[string]string{"name": "Alice"})
	assert.Equal(t, "Hi Alice, your code is {{code}}", got)
}

func TestApplyTemplate_EmptyVariables(t *testing.T) {
	got := ApplyTemplate("Hi {{name}}!", nil)
	assert.Equal(t, "Hi {{name}}!", got)
}

func TestApplyTemplate_NoPlaceholders(t *testing.T) {
	got := ApplyTemplate("Hello world", map[string]string{"name": "Alice"})
	assert.Equal(t, "Hello world", got)
}

func TestApplyTemplate_ReplacesAllOccurrences(t *testing.T) {
	got := ApplyTemplate("{{x}}-{{x}}-{{x}}", map[string]string{"x": "a"})
	assert.Equal(t, "a-a-a", got)
}

func TestApplyTemplate_SubstringKeysDoNotCollide(t *testing.T) {
	// Replacing "name" must not corrupt a "username" placeholder because the
	// implementation does literal {{key}} substitution.
	got := ApplyTemplate("{{name}} vs {{username}}",
		map[string]string{"name": "alice", "username": "alicesmith"})
	assert.Equal(t, "alice vs alicesmith", got)
}

func TestParseCSVBatch_HappyPath(t *testing.T) {
	csv := `to,name,code
+14155551111,Alice,A123
+14155552222,Bob,B456
`
	msgs, err := ParseCSVBatch(strings.NewReader(csv))
	require.NoError(t, err)
	require.Len(t, msgs, 2)

	assert.Equal(t, "+14155551111", msgs[0].To)
	assert.Equal(t, "Alice", msgs[0].Variables["name"])
	assert.Equal(t, "A123", msgs[0].Variables["code"])
	assert.Empty(t, msgs[0].Text, "no text column → empty")
}

func TestParseCSVBatch_TextColumnOverrides(t *testing.T) {
	csv := `to,text,name
+14155551111,Custom per-row text,Alice
`
	msgs, err := ParseCSVBatch(strings.NewReader(csv))
	require.NoError(t, err)
	require.Len(t, msgs, 1)
	assert.Equal(t, "Custom per-row text", msgs[0].Text)
}

func TestParseCSVBatch_ColumnAliases(t *testing.T) {
	// "phone" is a valid alias for "to", "body" is valid for "text".
	csv := `phone,body
+14155551111,Hello
`
	msgs, err := ParseCSVBatch(strings.NewReader(csv))
	require.NoError(t, err)
	require.Len(t, msgs, 1)
	assert.Equal(t, "+14155551111", msgs[0].To)
	assert.Equal(t, "Hello", msgs[0].Text)
}

func TestParseCSVBatch_HeaderIsCaseInsensitive(t *testing.T) {
	csv := `TO,Name
+14155551111,Alice
`
	msgs, err := ParseCSVBatch(strings.NewReader(csv))
	require.NoError(t, err)
	require.Len(t, msgs, 1)
	// Variable name preserves original case (the implementation trims lowercases
	// only for the to/text lookup; variables keep their original header).
	assert.Equal(t, "Alice", msgs[0].Variables["Name"])
}

func TestParseCSVBatch_MissingToColumn(t *testing.T) {
	csv := `name
Alice
`
	_, err := ParseCSVBatch(strings.NewReader(csv))
	assert.Error(t, err)
}

func TestParseCSVBatch_EmptyToIsSkipped(t *testing.T) {
	csv := `to,name
,Alice
+14155552222,Bob
`
	msgs, err := ParseCSVBatch(strings.NewReader(csv))
	require.NoError(t, err)
	require.Len(t, msgs, 1)
	assert.Equal(t, "+14155552222", msgs[0].To)
}

func TestParseCSVBatch_AllRowsInvalidReturnsError(t *testing.T) {
	csv := `to,name
,Alice
,Bob
`
	_, err := ParseCSVBatch(strings.NewReader(csv))
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "no valid message rows")
}
