<script lang="ts">
  import { t } from '../lib/i18n';

  let notice = $state(false);

  function submit(e: SubmitEvent): void {
    e.preventDefault();
    notice = true; // Placeholder until /api/v1 auth exists.
  }
</script>

<section class="card signin">
  <h1>{t('signIn.title')}</h1>
  <form onsubmit={submit} novalidate>
    <div class="field">
      <label for="username">{t('signIn.username')}</label>
      <input
        id="username"
        name="username"
        type="text"
        autocomplete="username"
        autocapitalize="none"
        spellcheck="false"
        required
      />
    </div>
    <div class="field">
      <label for="password">{t('signIn.password')}</label>
      <input id="password" name="password" type="password" autocomplete="current-password" required />
    </div>
    <button class="btn" type="submit">{t('signIn.submit')}</button>
    <p class="muted" role="status">{notice ? t('signIn.unavailable') : ''}</p>
  </form>
</section>

<style>
  .signin {
    max-inline-size: 420px;
    margin-inline: auto;
  }
  form {
    display: grid;
    gap: var(--space);
  }
  .field {
    display: grid;
    gap: 6px;
  }
  label {
    font-weight: 600;
    font-size: 0.9375rem;
  }
  input {
    min-block-size: var(--tap);
    padding-block: 10px;
    padding-inline: 12px;
    border: 1px solid var(--border);
    border-radius: var(--radius);
    background: var(--surface);
  }
  [role='status'] {
    margin: 0;
    min-block-size: 1.5em;
  }
</style>
