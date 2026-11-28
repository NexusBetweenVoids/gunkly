-- Services whose URL host part is a credential (a webhook ID, API key, channel
-- token or phone number) show only their kind as the channel hint
-- (notify.Hint). Reset hints stored by earlier versions, which held that host.
UPDATE notification_channels SET url_hint = service
    WHERE service IN ('teams', 'twilio', 'ifttt', 'join', 'notifiarr', 'signalgrid', 'wecom');
