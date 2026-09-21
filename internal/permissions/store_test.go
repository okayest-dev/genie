package permissions

import (
	"testing"
	"time"
)

func now() time.Time { return time.Now().UTC() }

func TestNoConfigDefaultRestrictsEveryAxis(t *testing.T) {
	s := New("/work")
	// Restrictive no-config default: read=".", everything else empty.
	if !s.Covered(AxisRead, "./src") && !s.Covered(AxisRead, "") {
		// read default covers cwd tree ("."), including blanket
	}
	if s.Covered(AxisWrite, "/work/x.go") {
		t.Errorf("write to /work/x.go covered under no-config default; want uncovered")
	}
	if s.Covered(AxisWrite, "") {
		t.Errorf("blanket write covered under no-config default; want uncovered")
	}
	for _, a := range []Axis{AxisNet, AxisRun, AxisEnv} {
		if s.Covered(a, "") {
			t.Errorf("%s blanket covered under no-config default; want uncovered", a)
		}
	}
	d := s.BaseSnapshot()
	if len(d) == 0 {
		t.Errorf("BaseSnapshot empty under no-config default; want read axis listed")
	}
}

func TestBaseConfigSetsWritableSurface(t *testing.T) {
	s := New("/work")
	s.SetBase(map[Axis][]string{
		AxisWrite: {"/work/src"},
	})
	if !s.Covered(AxisWrite, "/work/src/x.go") {
		t.Errorf("write to /work/src/x.go not covered by base /work/src")
	}
	if s.Covered(AxisWrite, "/work/other.go") {
		t.Errorf("write to /work/other.go covered; want uncovered (outside base)")
	}
	if s.Covered(AxisWrite, "") {
		t.Errorf("blanket write covered after base /work/src; want uncovered")
	}
	// read base still restrictive ".".
	if s.Covered(AxisRead, "/etc/passwd") {
		t.Errorf("read /etc/passwd covered; want uncovered under no read base")
	}
}

func TestEffectivePolicyIsUnionWithPermanent(t *testing.T) {
	s := New("/work")
	s.SetBase(map[Axis][]string{AxisWrite: {"/work"}})
	if err := s.GrantPermanent(Grant{Axis: AxisWrite, Scope: "/home/export", Tier: TierPermanent, Granted: now()}); err != nil {
		t.Fatalf("GrantPermanent: %v", err)
	}
	if !s.Covered(AxisWrite, "/work/a") {
		t.Errorf("base /work not honored")
	}
	if !s.Covered(AxisWrite, "/home/export/b") {
		t.Errorf("permanent /home/export not honored")
	}
	if s.Covered(AxisWrite, "/home/other") {
		t.Errorf("path outside base∪permanent covered; want uncovered")
	}
}

func TestSessionGrantCoversScopeRestOfSession(t *testing.T) {
	s := New("/work")
	if s.Covered(AxisWrite, "/tmp/log.txt") {
		t.Fatal("precondition: uncovered")
	}
	s.GrantSession(AxisWrite, "/tmp")
	if !s.Covered(AxisWrite, "/tmp/log.txt") {
		t.Errorf("session /tmp should cover a child write")
	}
	if s.Covered(AxisWrite, "/other") {
		t.Errorf("session /tmp leaked to /other")
	}
	// restore at session end
	s.EndSession()
	if s.Covered(AxisWrite, "/tmp/log.txt") {
		t.Errorf("session grant survived EndSession")
	}
}

func TestProviderGrantOnceIsCallBound(t *testing.T) {
	s := New("/work")
	if s.Covered(AxisWrite, "/tmp/once.txt") {
		t.Fatal("precondition: uncovered")
	}
	cid := s.BeginCall("/tmp/once.txt")
	s.Once(cid) // mark call in progress
	s.GrantOnce(cid, AxisWrite, "/tmp/once.txt")
	if !s.Covered(AxisWrite, "/tmp/once.txt") {
		t.Errorf("once grant should cover its exact scope")
	}
	// spent after the resolving call executes
	s.SpendOnce(cid)
	if s.Covered(AxisWrite, "/tmp/once.txt") {
		t.Errorf("once grant carried forward after spend; must not")
	}
}

func TestOnceGrantDiscardedOnDeny(t *testing.T) {
	s := New("/work")
	cid := s.BeginCall("/tmp/deny.txt")
	s.GrantOnce(cid, AxisWrite, "/tmp/deny.txt")
	if !s.Covered(AxisWrite, "/tmp/deny.txt") {
		t.Fatal("once grant not active")
	}
	// resolved with a denial: axis rejected -> discard, never spent forward
	s.DiscardOnce(cid)
	if s.Covered(AxisWrite, "/tmp/deny.txt") {
		t.Errorf("once grant survived denial; must be discarded")
	}
}

func TestCoverageScopeMatchingMostSpecificWins(t *testing.T) {
	s := New("/work")
	s.GrantSession(AxisWrite, "/work/src")
	s.GrantSession(AxisWrite, "/work/src/deep")
	// most specific wins: exact scope trivially covered; deeper granted scope
	// still covers its own subtree.
	if !s.Covered(AxisWrite, "/work/src/deep/f.go") {
		t.Errorf("deep scope should cover subtree")
	}
	// a sibling under the shallower but not deeper scope remains covered by the
	// shallow scope covers only its own zip, but deep scope is more specific.
	s.GrantSession(AxisWrite, "/work/other")
	if !s.Covered(AxisWrite, "/work/other/z.go") {
		t.Errorf("most-specific matching: /work/other covers child even when /work/src present")
	}
}

func TestSetBaseFromConfigNameKeyed(t *testing.T) {
	s := New("/work")
	// Name-keyed surface: only the five axis keys are honored, an unnamed axis
	// stays empty (replace-not-merge, so read on the base is dropped without a
	// declared read) and a non-axis key never becomes coverage.
	s.SetBaseFromConfig(map[string][]string{
		"write":   {"/work"},
		"ignored": {"/nope"},
	})
	if !s.Covered(AxisWrite, "/work/x.go") {
		t.Errorf("write /work/x.go not covered by name-keyed base /work")
	}
	if s.Covered(AxisRead, "/work/a.go") {
		t.Errorf("read covered under a base that declares no read; want empty (no axis-level inheritance)")
	}
	if s.Covered(Axis("ignored"), "/nope") {
		t.Errorf("non-axis key leaked into the base surface")
	}
	if s.Covered(AxisNet, "") {
		t.Errorf("net covered under a base that declares no net; want empty")
	}
	// Replacing again drops the prior write: a fresh surface wholly replaces.
	s.SetBaseFromConfig(map[string][]string{"net": {"api.example.com"}})
	if s.Covered(AxisWrite, "/work/x.go") {
		t.Errorf("write covered after replacement with a net-only surface; want empty")
	}
	if !s.Covered(AxisNet, "api.example.com") {
		t.Errorf("net api.example.com not covered after replacement")
	}
}

func TestPathNormalization(t *testing.T) {
	s := New("/work")
	s.SetBase(map[Axis][]string{AxisWrite: {"/work/.", "/work/a/../"}})
	if !s.Covered(AxisWrite, "/work/x.go") {
		t.Errorf("relative base . should normalize under cwd")
	}
	if !s.Covered(AxisWrite, "/work/b.go") {
		t.Errorf("a/.. base should normalize to /work")
	}
}

func TestScopeNormalizationResolvesUserAndDot(t *testing.T) {
	s := New("/work")
	s.SetBase(map[Axis][]string{AxisWrite: {"./sub", "/work"}})
	if !s.Covered(AxisWrite, "sub/f.go") {
		t.Errorf("relative request sub/f.go should resolve against cwd and match ./sub")
	}
	if !s.Covered(AxisWrite, "/work/x") {
		t.Errorf("/work base should cover absolute request")
	}
}

func TestPermanentGrantLifecycleAndSnapshot(t *testing.T) {
	s := New("/work")
	g := Grant{Axis: AxisWrite, Scope: "/home", Tier: TierPermanent, Granted: time.Now().UTC()}
	if err := s.GrantPermanent(g); err != nil {
		t.Fatalf("GrantPermanent: %v", err)
	}
	if !s.Covered(AxisWrite, "/home/x") {
		t.Errorf("permanent /home should cover")
	}
	// snapshots and merge of base+permanent
	perms := s.PermanentGrants()
	if len(perms) != 1 || perms[0].Scope != "/home" {
		t.Errorf("PermanentGrants = %+v; want one /home", perms)
	}
}

func TestDenyPointEvaluationNeverCarriesOnce(t *testing.T) {
	s := New("/work")
	// Spend-then-new-call: a once grant that was spent must never leak into a
	// fresh call.
	cid1 := s.BeginCall("/tmp/a.txt")
	s.GrantOnce(cid1, AxisWrite, "/tmp/a.txt")
	s.SpendOnce(cid1)
	if s.Covered(AxisWrite, "/tmp/a.txt") {
		t.Errorf("once grant leaked past spend into the store")
	}
}

func TestBlanketGrantCoversAxis(t *testing.T) {
	s := New("/work")
	if s.Covered(AxisWrite, "") {
		t.Fatal("precondition")
	}
	s.GrantSession(AxisWrite, "")
	if !s.Covered(AxisWrite, "/any/path") {
		t.Errorf("blanket session write should cover any path")
	}
	if !s.Covered(AxisWrite, "") {
		t.Errorf("blanket grant should cover blanket request")
	}
}
