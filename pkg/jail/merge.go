package jail

import (
	freebsdv1 "github.com/zachfi/nodemanager/api/freebsd/v1"
)

// MergeTemplateDefaults returns a copy of spec with zero-value fields filled
// from the template.  Jail-level values always take precedence.
//
// Merge rules:
//   - String fields: template value used when spec value is empty.
//   - Slice fields (Mounts): template used when spec slice is nil/empty;
//     no deep merge of individual entries.
//   - Struct fields (Update): merged field-by-field within the struct.
func MergeTemplateDefaults(spec freebsdv1.JailSpec, tmpl freebsdv1.JailTemplateSpec) freebsdv1.JailSpec {
	out := spec

	if out.Interface == "" {
		out.Interface = tmpl.Interface
	}

	if len(out.Mounts) == 0 {
		out.Mounts = tmpl.Mounts
	}

	out.Update = mergeUpdate(out.Update, tmpl.Update)

	return out
}

func mergeUpdate(spec, tmpl freebsdv1.JailUpdate) freebsdv1.JailUpdate {
	out := spec
	if out.Schedule == "" {
		out.Schedule = tmpl.Schedule
	}
	if out.Delay == "" {
		out.Delay = tmpl.Delay
	}
	if out.Group == "" {
		out.Group = tmpl.Group
	}
	return out
}
