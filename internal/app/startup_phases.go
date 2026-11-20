package app

import "sync"

// startupPhase is when a contribution runs. VS Code calls its earliest phase
// BlockStartup and its first frame AfterRestored; these are the two the app
// needs. Contributions choose a phase when they are registered, and the phase
// is run once by the startup path that reaches it.
type startupPhase uint8

const (
	startupBlockStartup startupPhase = iota
	startupAfterFirstFrame
)

type startupStep struct {
	mark string
	run  func()
}

// startupRunner owns the contributions registered for each phase. Registration
// happens before the first frame; run takes a snapshot so a step can register
// more work for a later phase without racing the current one.
type startupRunner struct {
	mu    sync.Mutex
	steps map[startupPhase][]startupStep
	ran   map[startupPhase]bool
}

func newStartupRunner() *startupRunner {
	return &startupRunner{
		steps: make(map[startupPhase][]startupStep),
		ran:   make(map[startupPhase]bool),
	}
}

func (p *startupRunner) add(phase startupPhase, mark string, fn func()) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.steps[phase] = append(p.steps[phase], startupStep{mark: mark, run: fn})
}

func (p *startupRunner) run(phase startupPhase) {
	p.mu.Lock()
	if p.ran[phase] {
		p.mu.Unlock()
		return
	}
	p.ran[phase] = true
	steps := append([]startupStep(nil), p.steps[phase]...)
	p.mu.Unlock()
	for _, step := range steps {
		step.run()
		if step.mark != "" {
			markStartup(step.mark)
		}
	}
}
