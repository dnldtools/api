package plans

import (
	"fmt"
	"time"
)

type Plan string

const (
	PlanTrial Plan = "trial"
	PlanFree  Plan = "free"
	PlanPro   Plan = "pro"
)

type Policy struct {
	RateLimit int

	RateWindow time.Duration

	DailyQuota int64

	MonthlyQuota int64
}

type PolicySet map[Plan]Policy

func Defaults() PolicySet {
	return PolicySet{
		PlanTrial: {
			RateLimit:    10,
			RateWindow:   time.Minute,
			DailyQuota:   100,
			MonthlyQuota: 1000,
		},
		PlanFree: {
			RateLimit:    60,
			RateWindow:   time.Minute,
			DailyQuota:   10_000,
			MonthlyQuota: 100_000,
		},
		PlanPro: {
			RateLimit:    600,
			RateWindow:   time.Minute,
			DailyQuota:   1_000_000,
			MonthlyQuota: 10_000_000,
		},
	}
}

func (s PolicySet) Get(plan Plan) Policy {
	if s == nil {
		return Defaults()[PlanTrial]
	}
	if p, ok := s[plan]; ok {
		return p
	}
	return s[PlanTrial]
}

func (s PolicySet) Validate() error {
	if s == nil {
		return fmt.Errorf("plans: nil policy set")
	}
	trial, ok := s[PlanTrial]
	if !ok {
		return fmt.Errorf("plans: missing %q fallback plan", PlanTrial)
	}
	for plan, p := range s {
		if err := validatePolicy(plan, p); err != nil {
			return err
		}
	}
	_ = trial
	return nil
}

func validatePolicy(plan Plan, p Policy) error {
	if p.RateWindow <= 0 {
		return fmt.Errorf("plans: plan %q has non-positive rate window", plan)
	}
	if p.RateLimit < 0 {
		return fmt.Errorf("plans: plan %q has negative rate limit", plan)
	}
	if p.DailyQuota < 0 {
		return fmt.Errorf("plans: plan %q has negative daily quota", plan)
	}
	if p.MonthlyQuota < 0 {
		return fmt.Errorf("plans: plan %q has negative monthly quota", plan)
	}
	return nil
}
