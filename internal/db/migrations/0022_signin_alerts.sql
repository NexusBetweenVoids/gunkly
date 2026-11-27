-- Email the user about a sign-in from an unusual place (new country, hosting
-- network). On by default; it only does anything when the instance has a
-- location source (GUNKLY_GEOIP_*). Purely additive.
ALTER TABLE users ADD COLUMN signin_alerts BOOLEAN NOT NULL DEFAULT TRUE;
