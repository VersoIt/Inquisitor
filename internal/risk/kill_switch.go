package risk

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"time"
)

type KillSwitchEventInput struct {
	EventID   string
	Active    bool
	Reason    string
	Source    string
	CreatedAt time.Time
}

type KillSwitchEvent struct {
	EventID   string
	Active    bool
	Reason    string
	Source    string
	CreatedAt time.Time
}

type KillSwitchState struct {
	Active    bool
	Reason    string
	Source    string
	UpdatedAt time.Time
}

type KillSwitchStats struct {
	Inserted int
	Skipped  int
}

type KillSwitchEventQuery struct {
	EventID string
	Active  *bool
	Source  string
	Start   time.Time
	End     time.Time
	Limit   int
}

type KillSwitchRepository interface {
	AppendKillSwitchEvent(ctx context.Context, event KillSwitchEvent) (KillSwitchStats, error)
	CurrentKillSwitchState(ctx context.Context) (KillSwitchState, error)
	ListKillSwitchEvents(ctx context.Context, query KillSwitchEventQuery) ([]KillSwitchEvent, error)
}

func NewKillSwitchEvent(input KillSwitchEventInput) (KillSwitchEvent, error) {
	event := KillSwitchEvent{
		EventID:   strings.TrimSpace(input.EventID),
		Active:    input.Active,
		Reason:    strings.TrimSpace(input.Reason),
		Source:    strings.ToLower(strings.TrimSpace(input.Source)),
		CreatedAt: input.CreatedAt.UTC(),
	}
	if err := ValidateKillSwitchEvent(event); err != nil {
		return KillSwitchEvent{}, err
	}
	return event, nil
}

func (s KillSwitchStats) Total() int {
	return s.Inserted + s.Skipped
}

func ValidateKillSwitchEvent(event KillSwitchEvent) error {
	var problems []string
	if strings.TrimSpace(event.EventID) == "" {
		problems = append(problems, "event_id is required")
	}
	if event.EventID != strings.TrimSpace(event.EventID) {
		problems = append(problems, "event_id must be trimmed")
	}
	if strings.TrimSpace(event.Reason) == "" {
		problems = append(problems, "reason is required")
	}
	if event.Reason != strings.TrimSpace(event.Reason) {
		problems = append(problems, "reason must be trimmed")
	}
	if strings.TrimSpace(event.Source) == "" {
		problems = append(problems, "source is required")
	}
	if event.Source != strings.ToLower(strings.TrimSpace(event.Source)) {
		problems = append(problems, "source must be lowercase and trimmed")
	}
	if event.CreatedAt.IsZero() {
		problems = append(problems, "created_at is required")
	}
	if len(problems) > 0 {
		return errors.New("kill switch event validation failed: " + strings.Join(problems, "; "))
	}
	return nil
}

func ValidateKillSwitchEvents(events []KillSwitchEvent) error {
	for index, event := range events {
		if err := ValidateKillSwitchEvent(event); err != nil {
			return errors.New("kill_switch_event[" + strconv.Itoa(index) + "]: " + err.Error())
		}
	}
	return nil
}

func ValidateKillSwitchState(state KillSwitchState) error {
	var problems []string
	if state.Active && state.UpdatedAt.IsZero() {
		problems = append(problems, "active kill switch requires updated_at")
	}
	if !state.UpdatedAt.IsZero() {
		if strings.TrimSpace(state.Reason) == "" {
			problems = append(problems, "state reason is required")
		}
		if state.Reason != strings.TrimSpace(state.Reason) {
			problems = append(problems, "state reason must be trimmed")
		}
		if strings.TrimSpace(state.Source) == "" {
			problems = append(problems, "state source is required")
		}
		if state.Source != strings.ToLower(strings.TrimSpace(state.Source)) {
			problems = append(problems, "state source must be lowercase and trimmed")
		}
	}
	if len(problems) > 0 {
		return errors.New("kill switch state validation failed: " + strings.Join(problems, "; "))
	}
	return nil
}

func ValidateKillSwitchEventQuery(query KillSwitchEventQuery) error {
	if strings.TrimSpace(query.Source) != "" && query.Source != strings.ToLower(strings.TrimSpace(query.Source)) {
		return errors.New("source must be lowercase and trimmed")
	}
	if !query.Start.IsZero() && !query.End.IsZero() && !query.End.After(query.Start) {
		return errors.New("end must be after start")
	}
	if query.Limit < 0 {
		return errors.New("limit must be greater than or equal to zero")
	}
	return nil
}
