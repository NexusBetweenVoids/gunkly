// Consent page enhancement (ADR 0016): "Confirm with a passkey". Without this
// script, or without WebAuthn in the browser, the page is the plain form.
// The assertion is only carried to the server in the form; the server
// verifies it itself. Nothing here can make a confirmation count.
(function () {
  'use strict';
  var btn = document.getElementById('oauth-passkey');
  if (!btn || !btn.form || !window.PublicKeyCredential || !navigator.credentials) return;
  var form = btn.form;
  var errBox = document.getElementById('oauth-passkey-error');
  var answer = document.getElementById('oauth-passkey-answer');
  var allow = form.querySelector('button[name="decision"][value="allow"]');
  if (!answer || !allow) return;
  btn.hidden = false;

  function fromB64u(s) {
    var b = atob(s.replace(/-/g, '+').replace(/_/g, '/'));
    var out = new Uint8Array(b.length);
    for (var i = 0; i < b.length; i++) out[i] = b.charCodeAt(i);
    return out.buffer;
  }
  function toB64u(buf) {
    var s = '';
    new Uint8Array(buf).forEach(function (x) { s += String.fromCharCode(x); });
    return btoa(s).replace(/\+/g, '-').replace(/\//g, '_').replace(/=+$/, '');
  }
  function fail() {
    errBox.textContent = btn.getAttribute('data-error');
    errBox.hidden = false;
    btn.disabled = false;
  }

  btn.addEventListener('click', function () {
    errBox.hidden = true;
    btn.disabled = true;
    var csrf = form.elements['csrf'].value;
    fetch(btn.getAttribute('data-options'), {
      method: 'POST',
      credentials: 'same-origin',
      headers: { 'Content-Type': 'application/x-www-form-urlencoded' },
      body: 'csrf=' + encodeURIComponent(csrf)
    }).then(function (res) {
      if (!res.ok) throw new Error('options');
      return res.json();
    }).then(function (o) {
      var pk = o.options.publicKey;
      pk.challenge = fromB64u(pk.challenge);
      (pk.allowCredentials || []).forEach(function (d) { d.id = fromB64u(d.id); });
      return navigator.credentials.get({ publicKey: pk }).then(function (c) {
        var r = c.response;
        answer.value = JSON.stringify({
          challenge: o.challenge,
          credential: {
            id: c.id, rawId: toB64u(c.rawId), type: c.type, clientExtensionResults: {},
            response: {
              clientDataJSON: toB64u(r.clientDataJSON),
              authenticatorData: toB64u(r.authenticatorData),
              signature: toB64u(r.signature),
              userHandle: r.userHandle ? toB64u(r.userHandle) : undefined
            }
          }
        });
        var pw = form.elements['password'];
        if (pw) pw.required = false;
        allow.click(); // the ordinary Allow post; the server checks the assertion
      });
    }).catch(fail);
  });
})();
