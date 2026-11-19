package markers

import (
	"cmp"
	"maps"
	"slices"
	"strconv"
	"strings"
	"sync"

	"github.com/baba983/vscroxy/internal/event"
)

// Severity has VS Code's MarkerSeverity values: Hint, Info, Warning and
// Error, in increasing importance. Read's Filter.Severities is a bit mask of
// these values.
type Severity uint16

const (
	Hint    Severity = 1
	Info    Severity = 2
	Warning Severity = 4
	Error   Severity = 8
)

// Range is a marker's optional 1-based range in its resource. An absent
// range is a marker about the resource as a whole.
type Range struct {
	StartLineNumber int
	StartColumn     int
	EndLineNumber   int
	EndColumn       int
}

// Marker is one problem. Owner and Resource are set by ChangeOne; producers
// supply the severity, message and, when it applies, a range. Code is VS
// Code's IMarkerData.code: the app uses it to name the field a click focuses.
type Marker struct {
	Owner, Resource string
	Severity        Severity
	Message         string
	Source          string
	Code            string
	Range           *Range
}

// Filter is VS Code's IMarkerReadOptions with Go types. An empty filter
// reads every marker; Owner and Resource narrow it to one owner or one
// resource, Severities is a bit mask, and Take limits the result.
type Filter struct {
	Owner, Resource string
	Severities      Severity
	Take            int
}

// Service stores markers by resource and owner, as VS Code's DoubleResourceMap
// does. ChangeOne replaces one (owner, resource) tuple; a different owner's
// markers on the same resource stay where they are.
type Service struct {
	OnMarkerChanged event.Event[[]string]

	mu       sync.Mutex
	data     map[string]map[string][]Marker
	onChange *event.Emitter[[]string]
}

// New returns a ready service.
func New() *Service {
	s := &Service{
		data:     make(map[string]map[string][]Marker),
		onChange: event.NewEmitter[[]string](),
	}
	s.OnMarkerChanged = s.onChange.Event
	return s
}

// ChangeOne is VS Code's changeOne(owner, resource, markerData). An empty
// list removes only this owner's entry and fires only when something was
// removed; a nonempty change always fires with exactly the one resource.
// Markers with an empty message are dropped, as in MarkerService._toMarker.
func (s *Service) ChangeOne(owner, resource string, next []Marker) {
	changed := false
	s.mu.Lock()
	if len(next) == 0 {
		if owners, ok := s.data[resource]; ok {
			if _, ok := owners[owner]; ok {
				delete(owners, owner)
				if len(owners) == 0 {
					delete(s.data, resource)
				}
				changed = true
			}
		}
	} else {
		markers := make([]Marker, 0, len(next))
		for _, marker := range next {
			if marker.Message == "" {
				continue
			}
			marker.Owner, marker.Resource = owner, resource
			marker.Range = normalizeRange(marker.Range)
			markers = append(markers, marker)
		}
		if len(markers) > 0 {
			if s.data[resource] == nil {
				s.data[resource] = make(map[string][]Marker)
			}
			s.data[resource][owner] = markers
			changed = true
		}
	}
	s.mu.Unlock()
	if changed {
		s.onChange.Fire([]string{resource})
	}
}

// Remove clears one owner's markers for each resource, as VS Code's remove.
func (s *Service) Remove(owner string, resources []string) {
	for _, resource := range resources {
		s.ChangeOne(owner, resource, nil)
	}
}

// Read returns a copy of the markers matching filter. Without a severity
// mask every severity matches; severities are combined with bitwise OR.
func (s *Service) Read(filter Filter) []Marker {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []Marker
	for resource, owners := range s.data {
		if filter.Resource != "" && resource != filter.Resource {
			continue
		}
		for owner, markers := range owners {
			if filter.Owner != "" && owner != filter.Owner {
				continue
			}
			for _, marker := range markers {
				if filter.Severities != 0 && marker.Severity&filter.Severities == 0 {
					continue
				}
				if filter.Take > 0 && len(out) >= filter.Take {
					return out
				}
				out = append(out, marker)
			}
		}
	}
	return out
}

// Resources lists the resources with markers, sorted by resource name.
func (s *Service) Resources() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return slices.Sorted(maps.Keys(s.data))
}

// Dispose releases the event's subscriptions.
func (s *Service) Dispose() {
	s.onChange.Dispose()
	s.mu.Lock()
	defer s.mu.Unlock()
	clear(s.data)
}

// CompareSeverity orders markers by VS Code's MarkerSeverity.compare: most
// severe first. Equal severities are left in the caller's order.
func CompareSeverity(a, b Marker) int {
	return cmp.Compare(b.Severity, a.Severity)
}

// Signature identifies a set of markers so a producer can skip a change
// that would report the same problems again.
func Signature(markers []Marker) string {
	var b strings.Builder
	for _, marker := range markers {
		b.WriteString(strconv.Itoa(int(marker.Severity)))
		b.WriteByte('¦')
		b.WriteString(marker.Code)
		b.WriteByte('¦')
		b.WriteString(marker.Message)
		b.WriteByte('¦')
		if marker.Range != nil {
			r := marker.Range
			b.WriteString(strconv.Itoa(r.StartLineNumber))
			b.WriteByte(':')
			b.WriteString(strconv.Itoa(r.StartColumn))
			b.WriteByte(':')
			b.WriteString(strconv.Itoa(r.EndLineNumber))
			b.WriteByte(':')
			b.WriteString(strconv.Itoa(r.EndColumn))
		}
		b.WriteByte('\n')
	}
	return b.String()
}

func normalizeRange(r *Range) *Range {
	if r == nil {
		return nil
	}
	out := *r
	if out.StartLineNumber <= 0 {
		out.StartLineNumber = 1
	}
	if out.StartColumn <= 0 {
		out.StartColumn = 1
	}
	if out.EndLineNumber < out.StartLineNumber {
		out.EndLineNumber = out.StartLineNumber
	}
	if out.EndColumn < out.StartColumn {
		out.EndColumn = out.StartColumn
	}
	return &out
}
