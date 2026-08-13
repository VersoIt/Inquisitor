package live

import (
	"errors"
	"fmt"
	"strings"
	"time"

	domainrisk "github.com/VersoIt/Inquisitor/internal/risk"
)

func ValidateKillSwitchReadinessArtifactHandoff(
	killSwitch domainrisk.KillSwitchArtifact,
	readiness LiveReadinessArtifact,
) error {
	if err := domainrisk.ValidateKillSwitchArtifact(killSwitch); err != nil {
		return err
	}
	if err := ValidateLiveReadinessArtifact(readiness); err != nil {
		return err
	}
	var problems []string
	if killSwitch.State == nil {
		problems = append(problems, "kill_switch_file.state is required")
	} else {
		if killSwitch.State.Active != readiness.KillSwitch.Active {
			problems = append(problems, fmt.Sprintf("kill_switch.active %t does not match readiness kill_switch.active %t", killSwitch.State.Active, readiness.KillSwitch.Active))
		}
		if killSwitch.State.Reason != readiness.KillSwitch.Reason {
			problems = append(problems, fmt.Sprintf("kill_switch.reason %q does not match readiness kill_switch.reason %q", killSwitch.State.Reason, readiness.KillSwitch.Reason))
		}
		if killSwitch.State.Source != readiness.KillSwitch.Source {
			problems = append(problems, fmt.Sprintf("kill_switch.source %q does not match readiness kill_switch.source %q", killSwitch.State.Source, readiness.KillSwitch.Source))
		}
		if !sameOptionalUTCTime(killSwitch.State.UpdatedAt, readiness.KillSwitch.UpdatedAt) {
			problems = append(problems, "kill_switch.updated_at does not match readiness kill_switch.updated_at")
		}
	}
	if len(problems) > 0 {
		return errors.New("kill switch readiness artifact handoff validation failed: " + strings.Join(problems, "; "))
	}
	return nil
}

func sameOptionalUTCTime(left *time.Time, right *time.Time) bool {
	if left == nil && right == nil {
		return true
	}
	if left == nil || right == nil {
		return false
	}
	return left.UTC().Equal(right.UTC())
}
