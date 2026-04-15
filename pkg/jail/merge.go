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
//   - PF: template rules are prepended to jail rules so the template can
//     establish a base policy that jails extend with service-specific rules.
func MergeTemplateDefaults(spec freebsdv1.JailSpec, tmpl freebsdv1.JailTemplateSpec) freebsdv1.JailSpec {
	out := spec

	if out.Interface == "" {
		out.Interface = tmpl.Interface
	}

	if len(out.Mounts) == 0 {
		out.Mounts = tmpl.Mounts
	}

	out.Update = mergeUpdate(out.Update, tmpl.Update)
	out.PF = mergePF(out.PF, tmpl.PF)

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

// mergePF merges template PF rules (base policy) with jail PF rules
// (service-specific extensions). Template rules are prepended so they form a
// base that the jail can build on. AnchorName from the jail takes precedence;
// if only the template sets one, it is inherited.
func mergePF(spec, tmpl *freebsdv1.JailPF) *freebsdv1.JailPF {
	if tmpl == nil {
		return spec
	}
	if spec == nil {
		out := *tmpl
		return &out
	}
	out := freebsdv1.JailPF{
		AnchorName: spec.AnchorName,
		Rules:      append(append([]string{}, tmpl.Rules...), spec.Rules...),
	}
	if out.AnchorName == "" {
		out.AnchorName = tmpl.AnchorName
	}
	return &out
}
