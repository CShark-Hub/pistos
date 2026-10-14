package ai

import (
	"errors"
	"fmt"
	"sync"

	"example.com/pistos/internal/config"
	"example.com/pistos/internal/finding"
)

// ErrBudget marks a call the per-review spend cap did not allow.
var ErrBudget = errors.New("skipped: AI budget")

// cost is a token count with its USD estimate; known is false when the model has no prices.
type cost struct {
	tokens int
	usd    float64
	known  bool
}

func (c cost) add(o cost) cost {
	return cost{tokens: c.tokens + o.tokens, usd: c.usd + o.usd, known: c.known && o.known}
}

func priceOf(m config.ModelAlias, prompt, completion int) cost {
	c := cost{tokens: prompt + completion}
	if m.Priced() {
		c.usd = float64(prompt)/1e6**m.PromptUSDPerMTok + float64(completion)/1e6**m.CompletionUSDPerMTok
		c.known = true
	}
	return c
}

// estimate is the conservative reservation for one attempt: every prompt character (byte) counted
// as a third of a token, plus the full completion allowance.
func estimate(m config.ModelAlias, msgs []Message, maxTokens int) cost {
	chars := 0
	for _, msg := range msgs {
		chars += len(msg.Content)
	}
	return priceOf(m, (chars+2)/3, maxTokens)
}

// meter enforces ai.budget.max_review_tokens / max_review_usd for one review. A call reserves its
// estimate before it is made; settling replaces the reservation by the reported usage. Attempts that
// fail, or succeed without reporting usage, keep their reservation: they may have been billed.
type meter struct {
	mu        sync.Mutex
	maxTokens int
	maxUSD    float64
	reserved  cost
	charged   cost
	spend     finding.AISpend
}

func newMeter(b config.AIBudget) *meter {
	return &meter{
		maxTokens: b.MaxReviewTokens, maxUSD: b.MaxReviewUSD,
		reserved: cost{known: true}, charged: cost{known: true},
		spend: finding.AISpend{MaxReviewTokens: b.MaxReviewTokens, MaxReviewUSD: b.MaxReviewUSD},
	}
}

// reserve holds c against the cap, or explains why it does not fit.
func (m *meter) reserve(c cost) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	next := m.charged.add(m.reserved).add(c)
	switch {
	case m.maxTokens > 0 && next.tokens > m.maxTokens:
		return fmt.Errorf("%w: ~%d more tokens would exceed max_review_tokens %d (%d already charged or reserved)", ErrBudget, c.tokens, m.maxTokens, next.tokens-c.tokens)
	case m.maxUSD > 0 && next.usd > m.maxUSD:
		return fmt.Errorf("%w: ~$%.4f more would exceed max_review_usd $%.4f ($%.4f already charged or reserved)", ErrBudget, c.usd, m.maxUSD, next.usd-c.usd)
	}
	m.reserved.tokens += c.tokens
	m.reserved.usd += c.usd
	return nil
}

// release drops held reservations of c each (attempts that were reserved but not made).
func (m *meter) release(c cost, held int) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.reserved.tokens -= c.tokens * held
	m.reserved.usd -= c.usd * float64(held)
}

// settle releases the held reservations of a call and charges it: the reported usage when it
// succeeded with usage, plus the reservation of every other attempt that reached the provider.
func (m *meter) settle(model config.ModelAlias, c cost, held, made int, ok bool, u Usage) {
	m.release(c, held)
	m.mu.Lock()
	defer m.mu.Unlock()
	m.spend.Calls++
	m.spend.PromptTokens += u.PromptTokens
	m.spend.CompletionTokens += u.CompletionTokens
	unreported := made
	if ok && u.PromptTokens+u.CompletionTokens > 0 {
		m.charged = m.charged.add(priceOf(model, u.PromptTokens, u.CompletionTokens))
		unreported = made - 1
	}
	for range unreported {
		m.charged = m.charged.add(c)
	}
}

func (m *meter) skipped(call string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.spend.Skipped = append(m.spend.Skipped, call)
}

func (m *meter) replayed() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.spend.Replayed++
}

func (m *meter) report() *finding.AISpend {
	m.mu.Lock()
	defer m.mu.Unlock()
	s := m.spend
	s.ChargedTokens, s.ChargedUSD, s.USDKnown = m.charged.tokens, m.charged.usd, m.charged.known
	s.Skipped = append([]string(nil), s.Skipped...)
	return &s
}
