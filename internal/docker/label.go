package docker

import (
	"encoding/json"
	"reflect"
	"strings"
)

const labelKey = "once"

// labelExtra carries the `once` label fields a newer version of Once wrote that
// this binary does not model, so rewriting the label preserves them instead of
// destroying them (as older binaries once did to "exposure"). A settings type
// embeds it to reach the codec: decodeLabel fills it, encodeLabel drains it.
//
// Equality checks on settings types deliberately ignore the carry. An older
// binary cannot edit fields it does not model, and a settings change that
// alters nothing short-circuits before the container roll, so the label is
// never rewritten from a value whose carry was not itself decoded from it.
type labelExtra struct {
	extra map[string]json.RawMessage
}

func (e *labelExtra) unknownLabelFields() map[string]json.RawMessage {
	return e.extra
}

func (e *labelExtra) setUnknownLabelFields(fields map[string]json.RawMessage) {
	e.extra = fields
}

// labelCarrier constrains the codec to settings types that embed labelExtra:
// PT's type term makes it *T, and only *T with the promoted carry methods
// satisfies it, so a settings type that forgets the embed fails to compile.
type labelCarrier[T any] interface {
	*T
	unknownLabelFields() map[string]json.RawMessage
	setUnknownLabelFields(map[string]json.RawMessage)
}

// decodeLabel unmarshals a `once` label into T, capturing the fields T does not
// model so a later encodeLabel can write them back. Fields named in removed
// were deliberately deleted from the schema, so they are shed rather than
// carried.
func decodeLabel[T any, PT labelCarrier[T]](label string, removed ...string) (T, error) {
	var settings T
	if err := json.Unmarshal([]byte(label), &settings); err != nil {
		return settings, err
	}

	unknown := unknownFields([]byte(label), reflect.TypeFor[T]())
	for _, field := range removed {
		delete(unknown, field)
	}
	PT(&settings).setUnknownLabelFields(unknown)

	return settings, nil
}

// encodeLabel marshals a settings value for the `once` label, merging back the
// unknown fields it carries. A field the struct models wins over a carried one
// of the same name, so this binary's value is what gets written - defensively,
// since decode never captures a key the struct claims in the first place.
func encodeLabel[T any, PT labelCarrier[T]](settings T) string {
	b, _ := json.Marshal(settings)

	unknown := PT(&settings).unknownLabelFields()
	if len(unknown) == 0 {
		return string(b)
	}

	var merged map[string]json.RawMessage
	if json.Unmarshal(b, &merged) != nil {
		return string(b)
	}
	for k, v := range unknown {
		if _, known := merged[k]; !known {
			merged[k] = v
		}
	}

	b, _ = json.Marshal(merged)
	return string(b)
}

// Helpers

// unknownFields returns the JSON object fields of data that no exported field
// of struct type t claims via its json tag - the fields a newer version of
// Once wrote that this binary doesn't model. Unexported fields claim nothing,
// so a label field named after one of them is preserved like any other.
func unknownFields(data []byte, t reflect.Type) map[string]json.RawMessage {
	var all map[string]json.RawMessage
	if json.Unmarshal(data, &all) != nil {
		return nil
	}
	for _, f := range reflect.VisibleFields(t) {
		if !f.IsExported() {
			continue
		}
		tag, _, _ := strings.Cut(f.Tag.Get("json"), ",")
		if tag == "" {
			tag = f.Name
		}
		delete(all, tag)
	}
	if len(all) == 0 {
		return nil
	}
	return all
}
