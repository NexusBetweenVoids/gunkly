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
	if s.metrics != nil {
		s.handle("GET /metrics", metricsToken, s.metricsHandler)
	}
	s.handle("GET /", public, s.serveStatic) // unknown /api/ paths get a JSON 404 there

	// First-run setup.
	s.handle("GET /api/v1/setup", public, s.getSetup)
	s.handle("POST /api/v1/setup", publicRateLimited(rateSetup), s.postSetup)

	// Sessions.
	s.handle("GET /api/v1/session", public, s.getSession)
	s.handle("POST /api/v1/session", publicRateLimited(rateLogin), s.login)
	s.handle("DELETE /api/v1/session", session.noToken(), s.logout)
	s.handle("POST /api/v1/session/step-up", session.noToken().withRate(rateTwoFA), s.stepUp)
	s.handle("GET /api/v1/sessions", session.noToken(), s.listSessions)
	s.handle("DELETE /api/v1/sessions", session.noToken(), s.revokeOtherSessions)
	s.handle("DELETE /api/v1/sessions/{id}", session.noToken(), s.revokeSession)

	// What this instance offers, and open sign-up with proof of work.
	s.handle("GET /api/v1/instance", public, s.getInstance)
	s.handle("GET /api/v1/signup/challenge", publicRateLimited(rateSignup), s.signupChallenge)
	s.handle("POST /api/v1/signup", publicRateLimited(rateSignup), s.signup)

	// Email: verification, address change, password reset, emailed codes.
	s.handle("POST /api/v1/account/email/verify", publicRateLimited(rateVerify), s.verifyEmail)
	s.handle("POST /api/v1/account/email/resend", session.noToken().withRate(rateEmail), s.resendVerification)
	s.handle("PATCH /api/v1/account/email", session.noToken().withStepUp().withRate(rateEmail), s.changeEmail)
	s.handle("POST /api/v1/password-reset/request", publicRateLimited(rateReset), s.requestPasswordReset)
	s.handle("POST /api/v1/password-reset/confirm", publicRateLimited(rateReset), s.confirmPasswordReset)
	s.handle("POST /api/v1/account/email-otp", session.noToken().withStepUp().withRate(rateTwoFA), s.enableEmailOTP)
	s.handle("DELETE /api/v1/account/email-otp", session.noToken().withStepUp(), s.disableEmailOTP)

	// Own account.
	s.handle("GET /api/v1/me", session.noToken(), s.getMe)
	s.handle("PATCH /api/v1/me", session.noToken(), s.patchMe)
	s.handle("POST /api/v1/account/password", session.noToken().withStepUp().withRate(ratePassword), s.changePassword)
	s.handle("POST /api/v1/account/totp/setup", session.noToken().withStepUp(), s.totpSetup)
	s.handle("POST /api/v1/account/totp/confirm", session.noToken().withRate(rateTwoFA), s.totpConfirm)
	s.handle("DELETE /api/v1/account/totp", session.noToken().withStepUp(), s.totpDisable)
	s.handle("GET /api/v1/account/audit", session.noToken(), s.listAudit)
	s.handle("GET /api/v1/account/deletion-preview", session.noToken(), s.accountDeletionPreview)
	s.handle("DELETE /api/v1/account", session.noToken().withStepUp(), s.deleteAccount)

	// Households, members, invites.
	s.handle("GET /api/v1/households", session.withToken(store.ScopeRead), s.listHouseholds)
	s.handle("POST /api/v1/households", session.noToken(), s.createHousehold)
	s.handle("GET /api/v1/households/{hh}", guest.withToken(store.ScopeRead), s.getHousehold)
	s.handle("PATCH /api/v1/households/{hh}", owner.noToken(), s.patchHousehold)
	s.handle("GET /api/v1/households/{hh}/members", guest.noToken(), s.listMembers)
	s.handle("PATCH /api/v1/households/{hh}/members/{user}", owner.noToken(), s.changeRole)
	s.handle("DELETE /api/v1/households/{hh}/members/{user}", guest.noToken(), s.removeMember) // owner, or self (store)
	s.handle("POST /api/v1/households/{hh}/invites", owner.noToken(), s.createInvite)
	s.handle("GET /api/v1/households/{hh}/invites", owner.noToken(), s.listInvites)
	s.handle("DELETE /api/v1/households/{hh}/invites/{invite}", owner.noToken(), s.revokeInvite)
	s.handle("POST /api/v1/invites/{token}/accept", session.noToken().withRate(rateInvite), s.acceptInvite)
	s.handle("POST /api/v1/invites/{token}/register", publicRateLimited(rateInvite), s.registerWithInvite)

	// Areas, chores and everything that happens to a chore.
	s.handle("GET /api/v1/templates", session.withToken(store.ScopeRead), s.listTemplates)
	s.handle("GET /api/v1/households/{hh}/areas", guest.withToken(store.ScopeRead), s.listAreas)
	s.handle("POST /api/v1/households/{hh}/areas", member.noToken(), s.createArea)
	s.handle("PATCH /api/v1/households/{hh}/areas/{area}", member.noToken(), s.renameArea)
	s.handle("DELETE /api/v1/households/{hh}/areas/{area}", member.noToken(), s.deleteArea)
	s.handle("GET /api/v1/households/{hh}/chores", guest.withToken(store.ScopeRead), s.listChores)
	s.handle("POST /api/v1/households/{hh}/chores", member.withToken(store.ScopeWrite), s.createChore)
	s.handle("GET /api/v1/households/{hh}/chores/{chore}", guest.withToken(store.ScopeRead), s.getChore)
	s.handle("PUT /api/v1/households/{hh}/chores/{chore}", member.withToken(store.ScopeWrite), s.updateChore)
	s.handle("DELETE /api/v1/households/{hh}/chores/{chore}", member.withToken(store.ScopeWrite), s.deleteChore)
	s.handle("POST /api/v1/households/{hh}/chores/{chore}/archive", member.withToken(store.ScopeWrite), s.archiveChore)
	s.handle("POST /api/v1/households/{hh}/chores/{chore}/unarchive", member.withToken(store.ScopeWrite), s.unarchiveChore)
	s.handle("POST /api/v1/households/{hh}/chores/{chore}/complete", member.withToken(store.ScopeComplete), s.completeChore)
	s.handle("GET /api/v1/households/{hh}/chores/{chore}/completions", guest.withToken(store.ScopeRead), s.listCompletions)
	s.handle("POST /api/v1/households/{hh}/chores/{chore}/completions/{completion}/revert", member.withToken(store.ScopeComplete), s.revertCompletion)
	s.handle("POST /api/v1/households/{hh}/chores/{chore}/snooze", member.withToken(store.ScopeComplete), s.snoozeChore)
	s.handle("POST /api/v1/households/{hh}/chores/{chore}/skip", member.withToken(store.ScopeComplete), s.skipChore)
	s.handle("POST /api/v1/households/{hh}/chores/{chore}/pause", member.withToken(store.ScopeComplete), s.pauseChore)
	s.handle("POST /api/v1/households/{hh}/chores/{chore}/resume", member.withToken(store.ScopeComplete), s.resumeChore)
	s.handle("GET /api/v1/households/{hh}/chores/{chore}/reminders", guest.withToken(store.ScopeRead), s.getReminders)
	s.handle("PUT /api/v1/households/{hh}/chores/{chore}/reminders", member.withToken(store.ScopeWrite), s.putReminders)
	s.handle("PUT /api/v1/households/{hh}/chores/{chore}/mute", member.withToken(store.ScopeWrite), s.muteChore)
	s.handle("DELETE /api/v1/households/{hh}/chores/{chore}/mute", member.withToken(store.ScopeWrite), s.unmuteChore)
	s.handle("GET /api/v1/households/{hh}/reminder-presets", guest.withToken(store.ScopeRead), s.listReminderPresets)
	s.handle("GET /api/v1/households/{hh}/calendar", guest.withToken(store.ScopeRead), s.calendar)

	// API tokens: the calling token's own description, and token management
	// (a session only: a token can never mint or list tokens).
	s.handle("GET /api/v1/token", policy{auth: authBearer}, s.getToken)
	s.handle("GET /api/v1/account/tokens", session.noToken(), s.listTokens)
	s.handle("POST /api/v1/account/tokens", session.noToken().withStepUp(), s.createToken)
	s.handle("DELETE /api/v1/account/tokens/{id}", session.noToken(), s.revokeToken)

	// MCP server for AI assistants (ADR 0015): Streamable HTTP, stateless, a bearer token only.
	for _, m := range []string{"POST", "GET", "DELETE"} {
		s.handle(m+" /mcp", policy{auth: authMCP}, s.mcpEndpoint())
	}

	// Instance administration: accounts and settings only, never household content.
	s.handle("GET /api/v1/admin/users", admin.noToken(), s.adminListUsers)
	s.handle("POST /api/v1/admin/users", admin.noToken().withStepUp(), s.adminCreateUser)
	s.handle("POST /api/v1/admin/users/{id}/reset-password", admin.noToken().withStepUp(), s.adminResetPassword)
	s.handle("GET /api/v1/admin/settings", admin.noToken(), s.adminGetSettings)
	s.handle("PATCH /api/v1/admin/settings", admin.noToken().withStepUp(), s.adminPatchSettings)
	s.handle("GET /api/v1/admin/updates", admin.noToken(), s.adminUpdates)

	s.registerLinkAndNotifyRoutes()
	s.registerTransferRoutes()
}

// registerLinkAndNotifyRoutes: unauthenticated links (mark-done pages, iCal),
// calendar feeds, notification channels, delivery logs. Kept in one block so
// it merges cleanly with the other route groups.
func (s *Server) registerLinkAndNotifyRoutes() {
	member, guest := household(store.RoleMember), household(store.RoleGuest)
	markDone, ical := publicRateLimited(rateMarkDone), publicRateLimited(rateICal)

	// Links that work without signing in (token-authenticated; see policy.tokenAuth).
	s.handle("GET /done/{token}", markDone, s.doneConfirm)
	s.handle("POST /done/{token}", markDone.withTokenAuth(), s.doneSubmit)
	s.handle("POST /done/undo/{token}", markDone.withTokenAuth(), s.doneUndo)
	s.handle("GET /done.css", public, s.doneStyle)
	s.handle("GET /ical/{file}", ical, s.icalFeed) // {file} is "<token>.ics"

	// Calendar feeds: read-only, so guests may have them.
	s.handle("GET /api/v1/households/{hh}/feeds", guest.noToken(), s.listFeeds)
	s.handle("POST /api/v1/households/{hh}/feeds", guest.noToken(), s.createFeed)
	s.handle("DELETE /api/v1/households/{hh}/feeds/{id}", guest.noToken(), s.deleteFeed)
	s.handle("POST /api/v1/households/{hh}/feeds/{id}/regenerate", guest.noToken(), s.regenerateFeed)

	// Notification channels: URLs are write-only.
	s.handle("GET /api/v1/notification-kinds", session.noToken(), s.listNotificationKinds)
	s.handle("GET /api/v1/account/channels", session.noToken(), s.listUserChannels)
	s.handle("POST /api/v1/account/channels", session.noToken(), s.createUserChannel)
	s.handle("PATCH /api/v1/account/channels/{id}", session.noToken(), s.patchUserChannel)
	s.handle("DELETE /api/v1/account/channels/{id}", session.noToken(), s.deleteUserChannel)
	s.handle("POST /api/v1/account/channels/{id}/test", session.noToken(), s.testUserChannel)
	s.handle("GET /api/v1/households/{hh}/channels", member.noToken(), s.listHouseholdChannels)
	s.handle("POST /api/v1/households/{hh}/channels", member.noToken(), s.createHouseholdChannel)
	s.handle("PATCH /api/v1/households/{hh}/channels/{id}", member.noToken(), s.patchHouseholdChannel)
	s.handle("DELETE /api/v1/households/{hh}/channels/{id}", member.noToken(), s.deleteHouseholdChannel)
	s.handle("POST /api/v1/households/{hh}/channels/{id}/test", member.noToken(), s.testHouseholdChannel)

	// Web Push (browser notifications): the VAPID key a browser subscribes
	// with, and this user's subscriptions.
	s.handle("GET /api/v1/push/vapid-public-key", session.noToken(), s.vapidPublicKey)
	s.handle("GET /api/v1/account/push-subscriptions", session.noToken(), s.listPushSubscriptions)
	s.handle("POST /api/v1/account/push-subscriptions", session.noToken(), s.createPushSubscription)
	s.handle("DELETE /api/v1/account/push-subscriptions/{id}", session.noToken(), s.deletePushSubscription)

	// Delivery logs.
	s.handle("GET /api/v1/account/deliveries", session.noToken(), s.listUserDeliveries)
	s.handle("GET /api/v1/households/{hh}/deliveries", member.noToken(), s.listHouseholdDeliveries)
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
