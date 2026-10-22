package app

import (
	"net/url"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"unicode/utf8"
)

// Field is a booking-form field (BKG-3). Name is the response key and query-parameter name (F-13).
type Field struct {
	Name           string   `json:"name"`
	Type           string   `json:"type"`
	Label          string   `json:"label"`
	Required       bool     `json:"required"`
	Hidden         bool     `json:"hidden"`
	Options        []string `json:"options,omitempty"`
	MinLength      *int     `json:"min_length,omitempty"`
	MaxLength      *int     `json:"max_length,omitempty"`
	AllowDomains   []string `json:"allow_domains,omitempty"`
	BlockDomains   []string `json:"block_domains,omitempty"`
	System         bool     `json:"system,omitempty"`
	RescheduleOnly bool     `json:"reschedule_only,omitempty"`
}

var fieldTypes = []string{"text", "textarea", "number", "email", "phone", "address", "url", "select", "multiselect", "checkbox", "radio", "boolean"}

var fieldNameRe = regexp.MustCompile(`^[a-z][a-z0-9_]{0,39}$`)

// systemFields are BKG-3's fixed fields, in form order.
func systemFields() []Field {
	return []Field{
		{Name: "name", Type: "text", Label: "Your name", Required: true, System: true},
		{Name: "email", Type: "email", Label: "Email address", Required: true, System: true},
		{Name: "phone", Type: "phone", Label: "Phone number", Hidden: true, System: true},
		{Name: "location", Type: "location", Label: "Location", System: true},
		{Name: "notes", Type: "textarea", Label: "Additional notes", System: true},
		{Name: "guests", Type: "guests", Label: "Add guests", System: true},
		{Name: "reschedule_reason", Type: "textarea", Label: "Reason for reschedule", System: true, RescheduleOnly: true},
	}
}

// lockedSystem fields can't be hidden or made optional (F-14).
var lockedSystem = map[string]bool{"name": true, "email": true, "reschedule_reason": true}

// validateFields checks the owner's stored fields: custom ones, and overrides of system fields
// (System = true) that may change only hidden/required and email domain lists.
func validateFields(fs []Field) error {
	seen := map[string]bool{}
	sys := map[string]bool{}
	for _, f := range systemFields() {
		sys[f.Name] = true
	}
	for _, f := range fs {
		if seen[f.Name] {
			return errf(ErrInvalid, "field %q twice", f.Name)
		}
		seen[f.Name] = true
		if f.System {
			if !sys[f.Name] {
				return errf(ErrInvalid, "unknown system field %q", f.Name)
			}
			if lockedSystem[f.Name] && (f.Hidden || !f.Required) && f.Name != "reschedule_reason" {
				return errf(ErrInvalid, "field %q can't be hidden or optional", f.Name)
			}
			continue
		}
		if sys[f.Name] || !fieldNameRe.MatchString(f.Name) {
			return errf(ErrInvalid, "field name %q: a-z, 0-9, _; not a system name", f.Name)
		}
		if !slices.Contains(fieldTypes, f.Type) {
			return errf(ErrInvalid, "field %q: type %q", f.Name, f.Type)
		}
		if strings.TrimSpace(f.Label) == "" {
			return errf(ErrInvalid, "field %q: label required", f.Name)
		}
		switch f.Type {
		case "select", "multiselect", "checkbox", "radio":
			if len(f.Options) == 0 {
				return errf(ErrInvalid, "field %q: options required", f.Name)
			}
		}
		if f.MinLength != nil && f.MaxLength != nil && *f.MinLength > *f.MaxLength {
			return errf(ErrInvalid, "field %q: min_length > max_length", f.Name)
		}
	}
	return nil
}

// FormFields is the effective form: system fields (with the owner's overrides) then custom fields.
func (et *EventType) FormFields() []Field {
	over := map[string]Field{}
	var custom []Field
	for _, f := range et.BookingFields {
		if f.System {
			over[f.Name] = f
		} else {
			custom = append(custom, f)
		}
	}
	var out []Field
	for _, f := range systemFields() {
		if o, ok := over[f.Name]; ok {
			f.Hidden, f.Required = o.Hidden, o.Required
			f.AllowDomains, f.BlockDomains = o.AllowDomains, o.BlockDomains
		}
		if f.Name == "location" && len(et.Locations) < 2 {
			f.Hidden = true // nothing to pick (EVT-14)
		}
		out = append(out, f)
	}
	return append(out, custom...)
}

var e164Re = regexp.MustCompile(`^\+[1-9][0-9]{6,14}$`)

// Responses are normalised form answers: string, []string, float64 or bool per field type.
type Responses map[string]any

// Str returns a string response or "".
func (r Responses) Str(k string) string { s, _ := r[k].(string); return s }

// List returns a list response.
func (r Responses) List(k string) []string {
	switch v := r[k].(type) {
	case []string:
		return v
	case []any:
		var out []string
		for _, x := range v {
			if s, ok := x.(string); ok {
				out = append(out, s)
			}
		}
		return out
	}
	return nil
}

// ParseResponses validates submitted values against the form (BKG-3, BKG-4). hostEmails are
// dropped from guests; reschedule enables reschedule-only fields.
func (et *EventType) ParseResponses(in url.Values, hostEmails []string, reschedule bool) (Responses, error) {
	out := Responses{}
	for _, f := range et.FormFields() {
		if f.RescheduleOnly && !reschedule {
			continue
		}
		raw := in[f.Name]
		var vals []string
		for _, v := range raw {
			if v = strings.TrimSpace(v); v != "" {
				vals = append(vals, v)
			}
		}
		required := f.Required && !f.Hidden // F-14: hidden fields are never required
		if f.Type == "boolean" {
			b := len(vals) > 0 && (vals[0] == "true" || vals[0] == "on" || vals[0] == "1" || vals[0] == "yes")
			if required && !b {
				return nil, errf(ErrFieldRequired, "%s", f.Name)
			}
			out[f.Name] = b
			continue
		}
		if f.Type == "guests" {
			if len(vals) == 1 && strings.Contains(vals[0], ",") {
				vals = strings.Split(vals[0], ",")
			}
		}
		if len(vals) == 0 {
			if required {
				return nil, errf(ErrFieldRequired, "%s", f.Name)
			}
			continue
		}
		switch f.Type {
		case "location":
			out[f.Name] = vals[0]
		case "guests":
			var guests []string
			for _, g := range vals {
				g = strings.TrimSpace(g)
				if g == "" {
					continue
				}
				e, err := NormalizeEmail(g)
				if err != nil {
					return nil, errf(ErrFieldInvalid, "guests: %q", g)
				}
				if !slices.Contains(guests, e) && !slices.Contains(hostEmails, e) {
					guests = append(guests, e)
				}
			}
			if len(guests) > 0 {
				out[f.Name] = guests
			}
		case "multiselect", "checkbox":
			for _, v := range vals {
				if !slices.Contains(f.Options, v) {
					return nil, errf(ErrFieldInvalid, "%s: %q not an option", f.Name, v)
				}
			}
			out[f.Name] = vals
		case "select", "radio":
			if !slices.Contains(f.Options, vals[0]) {
				return nil, errf(ErrFieldInvalid, "%s: %q not an option", f.Name, vals[0])
			}
			out[f.Name] = vals[0]
		case "number":
			n, err := strconv.ParseFloat(vals[0], 64)
			if err != nil {
				return nil, errf(ErrFieldInvalid, "%s: not a number", f.Name)
			}
			out[f.Name] = n
		default:
			v := vals[0]
			if err := checkText(f, v); err != nil {
				return nil, err
			}
			if f.Type == "email" {
				v = strings.ToLower(v)
			}
			out[f.Name] = v
		}
	}
	// guests also never include the booker (F-15)
	if g := out.List("guests"); g != nil {
		email := out.Str("email")
		var keep []string
		for _, x := range g {
			if x != email {
				keep = append(keep, x)
			}
		}
		if len(keep) == 0 {
			delete(out, "guests")
		} else {
			out["guests"] = keep
		}
	}
	return out, nil
}

func checkText(f Field, v string) error {
	n := utf8.RuneCountInString(v)
	if f.MinLength != nil && n < *f.MinLength {
		return errf(ErrFieldInvalid, "%s: at least %d characters", f.Name, *f.MinLength)
	}
	if f.MaxLength != nil && n > *f.MaxLength {
		return errf(ErrFieldInvalid, "%s: at most %d characters", f.Name, *f.MaxLength)
	}
	switch f.Type {
	case "email":
		e, err := NormalizeEmail(v)
		if err != nil {
			return errf(ErrFieldInvalid, "%s: not an email", f.Name)
		}
		domain := e[strings.LastIndex(e, "@")+1:]
		if len(f.AllowDomains) > 0 && !slices.ContainsFunc(f.AllowDomains, func(d string) bool { return strings.EqualFold(d, domain) }) {
			return errf(ErrFieldInvalid, "%s: domain not allowed", f.Name)
		}
		if slices.ContainsFunc(f.BlockDomains, func(d string) bool { return strings.EqualFold(d, domain) }) {
			return errf(ErrFieldInvalid, "%s: domain blocked", f.Name)
		}
	case "phone":
		if !e164Re.MatchString(v) {
			return errf(ErrFieldInvalid, "%s: not an E.164 phone number", f.Name)
		}
	case "url":
		u, err := url.Parse(v)
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
			return errf(ErrFieldInvalid, "%s: not a URL", f.Name)
		}
	}
	return nil
}
