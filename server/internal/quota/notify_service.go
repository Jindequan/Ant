package quota

import "context"

// maxPerPass keeps a burst (say, the first pass after a long sleep) from
// becoming a wall of notifications; the rest come a minute later.
const maxPerPass = 5

// notifyOnce shows every alert not shown yet, when notifications are on.
func (s *Service) notifyOnce(ctx context.Context) {
	if s.notifier == nil || !s.store.NotifyOn() {
		return
	}
	snap := s.Snapshot()
	var insights map[string][]Insight
	if s.insights != nil {
		insights, _ = s.insights.Insights(ctx) // alerts without history still work
	}
	s.mu.Lock()
	for _, a := range snap.Accounts { // signed in again: a later sign-out is news again
		if a.Status == StatusOK {
			delete(s.sent, a.ID+"|signed-out")
		}
	}
	var fresh []Alert
	for _, al := range alerts(snap.Accounts, insights, s.local.Now()) {
		if !s.sent[al.Key] && len(fresh) < maxPerPass {
			fresh = append(fresh, al)
			s.sent[al.Key] = true
		}
	}
	s.mu.Unlock()
	for _, al := range fresh {
		_ = s.notifier.Notify(al.Title, al.Body)
	}
}

// setNotify turns notifications on or off; turning them on shows one, so
// the user sees where they appear (and macOS can ask to allow them).
func (s *Service) setNotify(on bool) error {
	if err := s.store.SetNotify(on); err != nil {
		return err
	}
	if on && s.notifier != nil && !s.demo {
		// best effort: the setting is saved either way, and the page reflects it
		_ = s.notifier.Notify("Ant", "Ant will tell you here before anything runs out or is lost.")
	}
	return nil
}
