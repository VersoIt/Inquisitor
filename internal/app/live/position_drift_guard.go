package live

import (
	"context"
	"fmt"
	"strings"
	"time"

	domainlive "github.com/VersoIt/Inquisitor/internal/live"
	domainrisk "github.com/VersoIt/Inquisitor/internal/risk"
)

const LivePositionDriftKillSwitchSource = "live_position_drift"

type LivePositionDriftKillSwitchRequest struct {
	Report  LivePositionDriftReport
	EventID string
	Reason  string
	Source  string
}

type LivePositionDriftKillSwitchResult struct {
	Activated bool
	Event     domainrisk.KillSwitchEvent
}

func (s *Service) ActivateKillSwitchForBlockedPositionDrift(
	ctx context.Context,
	req LivePositionDriftKillSwitchRequest,
) (LivePositionDriftKillSwitchResult, error) {
	if err := ctx.Err(); err != nil {
		return LivePositionDriftKillSwitchResult{}, err
	}
	if err := domainlive.ValidateLiveOpsStatus(req.Report.Status); err != nil {
		return LivePositionDriftKillSwitchResult{}, err
	}
	if req.Report.Status != domainlive.LiveOpsStatusBlocked {
		return LivePositionDriftKillSwitchResult{}, nil
	}
	if s == nil {
		return LivePositionDriftKillSwitchResult{}, fmt.Errorf("position drift kill switch guard requires service")
	}
	if s.killSwitch == nil {
		return LivePositionDriftKillSwitchResult{}, fmt.Errorf("position drift kill switch guard requires kill switch repository")
	}
	if s.clock == nil {
		return LivePositionDriftKillSwitchResult{}, fmt.Errorf("position drift kill switch guard requires clock")
	}

	reason := strings.TrimSpace(req.Reason)
	if reason == "" {
		reason = LivePositionDriftKillSwitchReason(req.Report)
	}
	source := strings.TrimSpace(req.Source)
	if source == "" {
		source = LivePositionDriftKillSwitchSource
	}
	event, err := domainrisk.NewKillSwitchEvent(domainrisk.KillSwitchEventInput{
		EventID:   req.EventID,
		Active:    true,
		Reason:    reason,
		Source:    source,
		CreatedAt: s.clock.Now(),
	})
	if err != nil {
		return LivePositionDriftKillSwitchResult{}, err
	}
	if _, err := s.killSwitch.AppendKillSwitchEvent(ctx, event); err != nil {
		return LivePositionDriftKillSwitchResult{}, fmt.Errorf("activate kill switch for blocked position drift: %w", err)
	}
	return LivePositionDriftKillSwitchResult{Activated: true, Event: event}, nil
}

func LivePositionDriftKillSwitchReason(report LivePositionDriftReport) string {
	failedChecks := livePositionDriftFailedCheckNames(report.Checks)
	if len(failedChecks) == 0 {
		return "position drift status BLOCKED"
	}
	return "position drift BLOCKED: " + strings.Join(failedChecks, ", ")
}

func LivePositionDriftGeneratedKillSwitchEventID(now time.Time) string {
	return "risk_kill_switch_live_position_drift_" + now.UTC().Format("20060102T150405.000000000Z")
}

func livePositionDriftFailedCheckNames(checks []domainlive.ReadinessCheck) []string {
	seen := make(map[string]struct{})
	names := make([]string, 0, len(checks))
	for _, check := range checks {
		if check.Status != domainlive.ReadinessCheckStatusFail {
			continue
		}
		name := strings.TrimSpace(check.Name)
		if name == "" {
			continue
		}
		if _, ok := seen[name]; ok {
			continue
		}
		seen[name] = struct{}{}
		names = append(names, name)
	}
	return names
}
