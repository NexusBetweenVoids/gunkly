package api

import (
	"net/http"

	"github.com/NexusBetweenVoids/gunkly/internal/store"
)

// registerRoutes is the complete route table. Every route declares its
// policy; see router.go and the route-policy fitness test.
func (s *Server) registerRoutes() {
	owner, member, guest := household(store.RoleOwner), household(store.RoleMember), household(store.RoleGuest)

	// Probes and static files.
	s.handle("GET /healthz", public, s.healthz)
	s.handle("GET /readyz", public, s.readyz)
	s.handle("GET /", public, s.serveStatic) // unknown /api/ paths get a JSON 404 there

	// First-run setup.
	s.handle("GET /api/v1/setup", public, s.getSetup)
	s.handle("POST /api/v1/setup", publicRateLimited(rateSetup), s.postSetup)

	// Sessions.
	s.handle("GET /api/v1/session", public, s.getSession)
	s.handle("POST /api/v1/session", publicRateLimited(rateLogin), s.login)
	s.handle("DELETE /api/v1/session", session, s.logout)
	s.handle("POST /api/v1/session/step-up", session.withRate(rateTwoFA), s.stepUp)
	s.handle("GET /api/v1/sessions", session, s.listSessions)
	s.handle("DELETE /api/v1/sessions", session, s.revokeOtherSessions)
	s.handle("DELETE /api/v1/sessions/{id}", session, s.revokeSession)

	// Own account.
	s.handle("GET /api/v1/me", session, s.getMe)
	s.handle("PATCH /api/v1/me", session, s.patchMe)
	s.handle("POST /api/v1/account/password", session.withStepUp().withRate(ratePassword), s.changePassword)
	s.handle("POST /api/v1/account/totp/setup", session.withStepUp(), s.totpSetup)
	s.handle("POST /api/v1/account/totp/confirm", session.withRate(rateTwoFA), s.totpConfirm)
	s.handle("DELETE /api/v1/account/totp", session.withStepUp(), s.totpDisable)
	s.handle("GET /api/v1/account/audit", session, s.listAudit)

	// Households, members, invites.
	s.handle("GET /api/v1/households", session, s.listHouseholds)
	s.handle("POST /api/v1/households", session, s.createHousehold)
	s.handle("GET /api/v1/households/{hh}", guest, s.getHousehold)
	s.handle("PATCH /api/v1/households/{hh}", owner, s.patchHousehold)
	s.handle("GET /api/v1/households/{hh}/members", guest, s.listMembers)
	s.handle("PATCH /api/v1/households/{hh}/members/{user}", owner, s.changeRole)
	s.handle("DELETE /api/v1/households/{hh}/members/{user}", guest, s.removeMember) // owner, or self (store)
	s.handle("POST /api/v1/households/{hh}/invites", owner, s.createInvite)
	s.handle("GET /api/v1/households/{hh}/invites", owner, s.listInvites)
	s.handle("DELETE /api/v1/households/{hh}/invites/{invite}", owner, s.revokeInvite)
	s.handle("POST /api/v1/invites/{token}/accept", session.withRate(rateInvite), s.acceptInvite)
	s.handle("POST /api/v1/invites/{token}/register", publicRateLimited(rateInvite), s.registerWithInvite)

	// Areas, chores and everything that happens to a chore.
	s.handle("GET /api/v1/templates", session, s.listTemplates)
	s.handle("GET /api/v1/households/{hh}/areas", guest, s.listAreas)
	s.handle("POST /api/v1/households/{hh}/areas", member, s.createArea)
	s.handle("PATCH /api/v1/households/{hh}/areas/{area}", member, s.renameArea)
	s.handle("DELETE /api/v1/households/{hh}/areas/{area}", member, s.deleteArea)
	s.handle("GET /api/v1/households/{hh}/chores", guest, s.listChores)
	s.handle("POST /api/v1/households/{hh}/chores", member, s.createChore)
	s.handle("GET /api/v1/households/{hh}/chores/{chore}", guest, s.getChore)
	s.handle("PUT /api/v1/households/{hh}/chores/{chore}", member, s.updateChore)
	s.handle("DELETE /api/v1/households/{hh}/chores/{chore}", member, s.deleteChore)
	s.handle("POST /api/v1/households/{hh}/chores/{chore}/archive", member, s.archiveChore)
	s.handle("POST /api/v1/households/{hh}/chores/{chore}/unarchive", member, s.unarchiveChore)
	s.handle("POST /api/v1/households/{hh}/chores/{chore}/complete", member, s.completeChore)
	s.handle("GET /api/v1/households/{hh}/chores/{chore}/completions", guest, s.listCompletions)
	s.handle("POST /api/v1/households/{hh}/chores/{chore}/completions/{completion}/revert", member, s.revertCompletion)
	s.handle("POST /api/v1/households/{hh}/chores/{chore}/snooze", member, s.snoozeChore)
	s.handle("POST /api/v1/households/{hh}/chores/{chore}/skip", member, s.skipChore)
	s.handle("POST /api/v1/households/{hh}/chores/{chore}/pause", member, s.pauseChore)
	s.handle("POST /api/v1/households/{hh}/chores/{chore}/resume", member, s.resumeChore)
	s.handle("GET /api/v1/households/{hh}/chores/{chore}/reminders", guest, s.getReminders)
	s.handle("PUT /api/v1/households/{hh}/chores/{chore}/reminders", member, s.putReminders)
	s.handle("PUT /api/v1/households/{hh}/chores/{chore}/mute", member, s.muteChore)
	s.handle("DELETE /api/v1/households/{hh}/chores/{chore}/mute", member, s.unmuteChore)
	s.handle("GET /api/v1/households/{hh}/reminder-presets", guest, s.listReminderPresets)
	s.handle("GET /api/v1/households/{hh}/calendar", guest, s.calendar)

	// Instance administration: accounts and settings only, never household content.
	s.handle("GET /api/v1/admin/users", admin, s.adminListUsers)
	s.handle("POST /api/v1/admin/users", admin.withStepUp(), s.adminCreateUser)
	s.handle("POST /api/v1/admin/users/{id}/reset-password", admin.withStepUp(), s.adminResetPassword)
	s.handle("GET /api/v1/admin/settings", admin, s.adminGetSettings)
	s.handle("PATCH /api/v1/admin/settings", admin.withStepUp(), s.adminPatchSettings)
}

func (s *Server) healthz(w http.ResponseWriter, _ *http.Request, _ *call) error {
	return ok(w, map[string]string{"status": "ok"})
}

func (s *Server) readyz(w http.ResponseWriter, r *http.Request, _ *call) error {
	if err := s.ready(r.Context()); err != nil {
		s.log.Warn("not ready", "err", err)
		return errf(http.StatusServiceUnavailable, "not_ready")
	}
	return ok(w, map[string]string{"status": "ready"})
}
