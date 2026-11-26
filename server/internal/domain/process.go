package domain

const (
	CheckKindPreview          = "preview"
	CheckKindPreflight        = "preflight"
	CheckKindRuntimeProbe     = "runtime_probe"
	CheckKindRuntimeOperation = "runtime_operation"
	CheckKindResourceStatus   = "resource_status"
	CheckKindDeleteImpact     = "delete_impact"
	CheckKindBackupRestore    = "backup_restore"
	CheckKindTask             = "task"

	CheckStatusPending   = "pending"
	CheckStatusRunning   = "running"
	CheckStatusPassed    = "passed"
	CheckStatusWarning   = "warning"
	CheckStatusBlocked   = "blocked"
	CheckStatusFailed    = "failed"
	CheckStatusCancelled = "cancelled"

	CheckOutcomePassed  = "passed"
	CheckOutcomeWarning = "warning"
	CheckOutcomeFailed  = "failed"
	CheckOutcomeSkipped = "skipped"

	CheckSeverityInfo    = "info"
	CheckSeverityWarning = "warning"
	CheckSeverityDanger  = "danger"

	ResourceActionCreate  = "create"
	ResourceActionUpdate  = "update"
	ResourceActionDelete  = "delete"
	ResourceActionExecute = "execute"
	ResourceActionBind    = "bind"
	ResourceActionUnbind  = "unbind"
	ResourceActionInspect = "inspect"
	ResourceActionBackup  = "backup"
	ResourceActionRestore = "restore"
)

type ResourceRef struct {
	Kind   string            `json:"kind"`
	ID     string            `json:"id"`
	Name   string            `json:"name,omitempty"`
	Scope  string            `json:"scope,omitempty"`
	Labels map[string]string `json:"labels,omitempty"`
}

type CheckDecision struct {
	Allowed  bool     `json:"allowed"`
	Blockers []string `json:"blockers"`
	Warnings []string `json:"warnings"`
}

type CheckItem struct {
	ID         string      `json:"id"`
	Code       string      `json:"code"`
	Category   string      `json:"category"`
	Phase      string      `json:"phase"`
	Target     ResourceRef `json:"target"`
	Outcome    string      `json:"outcome"`
	Severity   string      `json:"severity"`
	Blocking   bool        `json:"blocking"`
	Summary    string      `json:"summary"`
	Details    Object      `json:"details"`
	ObservedAt string      `json:"observedAt"`
}

type ResourceImpact struct {
	Action      string      `json:"action"`
	Resource    ResourceRef `json:"resource"`
	Count       int         `json:"count"`
	Severity    string      `json:"severity"`
	ReasonCodes []string    `json:"reasonCodes"`
}

type ResourceLink struct {
	Source   ResourceRef `json:"source"`
	Target   ResourceRef `json:"target"`
	Relation string      `json:"relation"`
	Metadata Object      `json:"metadata,omitempty"`
}

type CheckReport struct {
	ID          string           `json:"id"`
	Kind        string           `json:"kind"`
	Status      string           `json:"status"`
	Subject     ResourceRef      `json:"subject"`
	Decision    CheckDecision    `json:"decision"`
	Checks      []CheckItem      `json:"checks"`
	Impacts     []ResourceImpact `json:"impacts"`
	Resources   []ResourceRef    `json:"resources"`
	Metrics     Object           `json:"metrics"`
	StartedAt   string           `json:"startedAt,omitempty"`
	CompletedAt string           `json:"completedAt,omitempty"`
	GeneratedAt string           `json:"generatedAt"`
}

type CheckReportBuilder struct {
	report   CheckReport
	blockers []string
	warnings []string
}

func NewCheckReportBuilder(kind string, subject ResourceRef) *CheckReportBuilder {
	now := Now()
	report := CheckReport{
		ID:          NewID("proc"),
		Kind:        normalizeCheckKind(kind),
		Status:      CheckStatusPending,
		Subject:     NormalizeResourceRef(subject),
		Decision:    CheckDecision{Allowed: true, Blockers: []string{}, Warnings: []string{}},
		Checks:      []CheckItem{},
		Impacts:     []ResourceImpact{},
		Resources:   []ResourceRef{},
		Metrics:     Object{},
		GeneratedAt: now,
	}
	return (&CheckReportBuilder{report: report}).AddResource(subject)
}

func (b *CheckReportBuilder) WithID(id string) *CheckReportBuilder {
	if id != "" {
		b.report.ID = id
	}
	return b
}

func (b *CheckReportBuilder) StartedAt(value string) *CheckReportBuilder {
	b.report.StartedAt = value
	return b
}

func (b *CheckReportBuilder) CompletedAt(value string) *CheckReportBuilder {
	b.report.CompletedAt = value
	return b
}

func (b *CheckReportBuilder) Metrics(metrics Object) *CheckReportBuilder {
	if metrics == nil {
		metrics = Object{}
	}
	b.report.Metrics = CloneObject(metrics)
	return b
}

func (b *CheckReportBuilder) AddResource(ref ResourceRef) *CheckReportBuilder {
	ref = NormalizeResourceRef(ref)
	if ref.Kind == "" || ref.ID == "" {
		return b
	}
	for _, existing := range b.report.Resources {
		if existing.Kind == ref.Kind && existing.ID == ref.ID {
			return b
		}
	}
	b.report.Resources = append(b.report.Resources, ref)
	return b
}

func (b *CheckReportBuilder) Check(code string, category string, phase string, target ResourceRef, outcome string, severity string, blocking bool, summary string, details Object) *CheckReportBuilder {
	target = NormalizeResourceRef(target)
	if target.Kind == "" || target.ID == "" {
		target = b.report.Subject
	}
	check := CheckItem{
		ID:         NewID("chk"),
		Code:       code,
		Category:   category,
		Phase:      phase,
		Target:     target,
		Outcome:    normalizeCheckOutcome(outcome),
		Severity:   normalizeCheckSeverity(severity),
		Blocking:   blocking,
		Summary:    summary,
		Details:    CloneObject(details),
		ObservedAt: Now(),
	}
	if check.Details == nil {
		check.Details = Object{}
	}
	b.report.Checks = append(b.report.Checks, check)
	return b.AddResource(target)
}

func (b *CheckReportBuilder) Blocker(code string) *CheckReportBuilder {
	if code != "" {
		b.blockers = append(b.blockers, code)
	}
	return b
}

func (b *CheckReportBuilder) Warning(code string) *CheckReportBuilder {
	if code != "" {
		b.warnings = append(b.warnings, code)
	}
	return b
}

func (b *CheckReportBuilder) Impact(action string, resource ResourceRef, count int, severity string, reasonCodes ...string) *CheckReportBuilder {
	resource = NormalizeResourceRef(resource)
	if resource.Kind == "" || resource.ID == "" {
		return b
	}
	if count <= 0 {
		count = 1
	}
	b.report.Impacts = append(b.report.Impacts, ResourceImpact{
		Action:      normalizeResourceAction(action),
		Resource:    resource,
		Count:       count,
		Severity:    normalizeCheckSeverity(severity),
		ReasonCodes: append([]string(nil), reasonCodes...),
	})
	return b.AddResource(resource)
}

func (b *CheckReportBuilder) Report() CheckReport {
	report := b.report
	blockers := append([]string(nil), b.blockers...)
	warnings := append([]string(nil), b.warnings...)
	hasBlockingFailure := false
	hasNonBlockingFailure := false
	hasWarning := false
	for _, check := range report.Checks {
		switch check.Outcome {
		case CheckOutcomeFailed:
			if check.Blocking {
				hasBlockingFailure = true
				blockers = append(blockers, check.Code)
			} else {
				hasNonBlockingFailure = true
				warnings = append(warnings, check.Code)
			}
		case CheckOutcomeWarning:
			hasWarning = true
			warnings = append(warnings, check.Code)
		}
	}
	report.Decision = CheckDecision{
		Allowed:  len(blockers) == 0,
		Blockers: uniqueNonEmptyStrings(blockers),
		Warnings: uniqueNonEmptyStrings(warnings),
	}
	switch {
	case len(report.Decision.Blockers) > 0 || hasBlockingFailure:
		report.Status = CheckStatusBlocked
	case hasNonBlockingFailure || hasWarning || len(report.Decision.Warnings) > 0:
		report.Status = CheckStatusWarning
	default:
		report.Status = CheckStatusPassed
	}
	if report.Resources == nil {
		report.Resources = []ResourceRef{}
	}
	if report.Checks == nil {
		report.Checks = []CheckItem{}
	}
	if report.Impacts == nil {
		report.Impacts = []ResourceImpact{}
	}
	if report.Metrics == nil {
		report.Metrics = Object{}
	}
	if report.GeneratedAt == "" {
		report.GeneratedAt = Now()
	}
	return report
}

func NormalizeResourceRef(ref ResourceRef) ResourceRef {
	if ref.Labels != nil && len(ref.Labels) == 0 {
		ref.Labels = nil
	}
	return ref
}

func Resource(kind string, id string, name string) ResourceRef {
	return ResourceRef{Kind: kind, ID: id, Name: name}
}

func CheckObject(report CheckReport) Object {
	value, err := EncodeObject(report)
	if err != nil {
		return Object{}
	}
	return value
}

func normalizeCheckKind(value string) string {
	switch value {
	case CheckKindPreview, CheckKindPreflight, CheckKindRuntimeProbe, CheckKindRuntimeOperation, CheckKindResourceStatus, CheckKindDeleteImpact, CheckKindBackupRestore, CheckKindTask:
		return value
	default:
		return CheckKindPreview
	}
}

func normalizeCheckOutcome(value string) string {
	switch value {
	case CheckOutcomePassed, CheckOutcomeWarning, CheckOutcomeFailed, CheckOutcomeSkipped:
		return value
	default:
		return CheckOutcomePassed
	}
}

func normalizeCheckSeverity(value string) string {
	switch value {
	case CheckSeverityInfo, CheckSeverityWarning, CheckSeverityDanger:
		return value
	default:
		return CheckSeverityInfo
	}
}

func normalizeResourceAction(value string) string {
	switch value {
	case ResourceActionCreate, ResourceActionUpdate, ResourceActionDelete, ResourceActionExecute, ResourceActionBind, ResourceActionUnbind, ResourceActionInspect, ResourceActionBackup, ResourceActionRestore:
		return value
	default:
		return ResourceActionInspect
	}
}

func uniqueNonEmptyStrings(values []string) []string {
	result := []string{}
	seen := map[string]bool{}
	for _, value := range values {
		if value == "" || seen[value] {
			continue
		}
		seen[value] = true
		result = append(result, value)
	}
	return result
}
