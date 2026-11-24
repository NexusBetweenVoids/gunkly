<script lang="ts">
  import Alert from '../../components/Alert.svelte';
  import Button from '../../components/Button.svelte';
  import Dialog from '../../components/Dialog.svelte';
  import { api } from '../../lib/api';
  import { copyText } from '../../lib/clip';
  import { failure } from '../../lib/errors';
  import { t, type Key } from '../../lib/i18n';
  import { app } from '../../lib/session.svelte';
  import { withStepUp } from '../../lib/stepup.svelte';
  import type { Household } from '../../lib/types';

  let { households, oncreated }: { households: Household[]; oncreated: () => void } = $props();

  type Kind = 'code' | 'desktop' | 'gemini' | 'other' | 'chatgpt' | 'claude';
  const kinds: { id: Kind; label: Key; name: string }[] = [
    { id: 'code', label: 'mcp.kindCode', name: 'Claude Code' },
    { id: 'desktop', label: 'mcp.kindDesktop', name: 'Claude Desktop' },
    { id: 'gemini', label: 'mcp.kindGemini', name: 'Gemini CLI' },
    { id: 'other', label: 'mcp.kindOther', name: 'MCP app' },
    { id: 'chatgpt', label: 'mcp.kindChatgpt', name: 'ChatGPT' },
    { id: 'claude', label: 'mcp.kindClaudeConnector', name: 'Claude' },
  ];

  let open = $state(false);
  let kind = $state<Kind>('code');
  let picked = $state<string[]>([]);
  let canComplete = $state(true);
  let canWrite = $state(false);
  let secret = $state('');
  let busy = $state(false);
  let err = $state('');
  let copied = $state('');

  const base = $derived((app.instance.baseUrl || location.origin).replace(/\/+$/, ''));
  const url = $derived(`${base}/mcp`);
  // ChatGPT and Claude connectors sign in with OAuth: no access key, so no token is created.
  const connector = $derived(kind === 'chatgpt' || kind === 'claude');
  const plainHttp = $derived(url.startsWith('http://') && !/^http:\/\/(localhost|127\.0\.0\.1|\[::1\])(:|\/)/.test(url));
  const codeCmd = $derived(`claude mcp add --transport http gunkly ${url} --header "Authorization: Bearer ${secret}"`);
  // mcp-remote: a space inside a --header argument is mangled by some shells
  // and by Windows, so the value comes from an environment variable.
  const desktopJson = $derived(
    JSON.stringify(
      {
        mcpServers: {
          gunkly: {
            command: 'npx',
            args: ['-y', 'mcp-remote', url, '--header', 'Authorization:${GUNKLY_AUTH}', ...(plainHttp ? ['--allow-http'] : [])],
            env: { GUNKLY_AUTH: `Bearer ${secret}` },
          },
        },
      },
      null,
      2,
    ),
  );
  const geminiJson = $derived(
    JSON.stringify({ mcpServers: { gunkly: { httpUrl: url, headers: { Authorization: `Bearer ${secret}` } } } }, null, 2),
  );
  const prompts: Key[] = ['mcp.prompt1', 'mcp.prompt2', 'mcp.prompt3'];

  function show(): void {
    kind = 'code';
    picked = households.map((h) => h.id);
    canComplete = true;
    canWrite = false;
    err = '';
    copied = '';
    open = true;
  }

  function close(): void {
    open = false;
    secret = ''; // the secret leaves memory with the dialog
  }

  async function create(e: SubmitEvent): Promise<void> {
    e.preventDefault();
    if (!picked.length) return void (err = t('tokens.needHousehold'));
    const scopes = ['chores:read', ...(canComplete ? ['chores:complete'] : []), ...(canWrite ? ['chores:write'] : [])];
    const label = kinds.find((k) => k.id === kind)!.name;
    busy = true;
    err = '';
    try {
      const made = await withStepUp(() =>
        api<{ token: string }>('POST', '/account/tokens', { name: `${label} (MCP)`, scopes, households: picked, expiresInDays: 365 }),
      );
      secret = made.token;
      oncreated();
    } catch (x) {
      err = failure(x).message;
    } finally {
      busy = false;
    }
  }

  async function copy(text: string, what: string): Promise<void> {
    await copyText(text);
    copied = t('mcp.copiedWhat', { what });
  }
</script>

<section class="card" aria-labelledby="a-mcp">
  <h2 id="a-mcp">{t('mcp.title')}</h2>
  {#if app.instance.mcpEnabled === false}
    <p class="muted">{t('mcp.disabled')}</p>
  {:else}
    <ul class="bullets">
      <li>{t('mcp.point1')}</li>
      <li>{t('mcp.point2')}</li>
      <li>{t('mcp.point3')}</li>
    </ul>
    {#if households.length === 0}
      <p class="hint">{t('tokens.needHouseholdFirst')}</p>
    {:else}
      <Button onclick={show}>{t('mcp.start')}</Button>
    {/if}
  {/if}
</section>

<Dialog {open} title={secret ? t('mcp.readyTitle') : t('mcp.title')} onclose={close}>
  {#if secret}
    <div class="stack">
      <Alert message={t('mcp.once')} />
      {#if kind === 'code'}
        <ol class="steps stack-sm">
          <li>{t('mcp.codeStep1')}</li>
          <li>
            {t('mcp.codeStep2')}
            <pre class="code">{codeCmd}</pre>
            <Button onclick={() => copy(codeCmd, t('mcp.theCommand'))}>{t('mcp.copyCommand')}</Button>
          </li>
          <li>{t('mcp.codeStep3')}</li>
        </ol>
      {:else if kind === 'desktop'}
        <ol class="steps stack-sm">
          <li>{t('mcp.deskStep1')}</li>
          <li>{t('mcp.deskStep2')}</li>
          <li>
            {t('mcp.deskStep3')}
            <pre class="code">{desktopJson}</pre>
            <Button onclick={() => copy(desktopJson, t('mcp.theSettings'))}>{t('mcp.copySettings')}</Button>
          </li>
          <li>{t('mcp.deskStep4')}</li>
        </ol>
        <p class="hint">{t('mcp.deskNode')}</p>
        {#if plainHttp}<p class="hint">{t('mcp.plainHttp')}</p>{/if}
      {:else if kind === 'gemini'}
        <ol class="steps stack-sm">
          <li>{t('mcp.gemStep1')}</li>
          <li>
            {t('mcp.gemStep2')}
            <pre class="code">{geminiJson}</pre>
            <Button onclick={() => copy(geminiJson, t('mcp.theSettings'))}>{t('mcp.copySettings')}</Button>
          </li>
          <li>{t('mcp.gemStep3')}</li>
        </ol>
      {:else}
        <p>{t('mcp.otherIntro')}</p>
        <p class="hint">{t('mcp.otherApps')}</p>
        <dl class="facts">
          <dt>{t('mcp.fieldUrl')}</dt>
          <dd><span class="mono">{url}</span> <Button variant="quiet" onclick={() => copy(url, t('mcp.fieldUrl'))}>{t('common.copy')}</Button></dd>
          <dt>{t('mcp.fieldTransport')}</dt>
          <dd>Streamable HTTP</dd>
          <dt>{t('mcp.fieldHeader')}</dt>
          <dd><span class="mono">Authorization</span></dd>
          <dt>{t('mcp.fieldValue')}</dt>
          <dd><span class="mono">Bearer {secret}</span> <Button variant="quiet" onclick={() => copy(`Bearer ${secret}`, t('mcp.fieldValue'))}>{t('common.copy')}</Button></dd>
        </dl>
      {/if}
      <div class="visually-hidden" aria-live="polite">{copied}</div>
      <h3>{t('mcp.tryTitle')}</h3>
      <ul class="bullets">
        {#each prompts as p (p)}<li>{t(p)}</li>{/each}
      </ul>
      <p class="hint">{t('mcp.revokeHint')}</p>
      <div class="row"><Button onclick={close}>{t('common.done')}</Button></div>
    </div>
  {:else}
    <form class="stack" onsubmit={create}>
      <fieldset class="plain-fieldset stack-sm">
        <legend class="label">{t('mcp.which')}</legend>
        {#each kinds as k (k.id)}
          <label class="check"><input type="radio" name="mcp-kind" value={k.id} bind:group={kind} />{t(k.label)}</label>
        {/each}
      </fieldset>
      {#if connector}
        {#if app.instance.oauthEnabled}
          <p>{t('mcp.connSteps', { app: kinds.find((k) => k.id === kind)!.name })}</p>
          <p><span class="label">{t('mcp.connUrl')}</span> <span class="mono">{url}</span> <Button variant="quiet" onclick={() => copy(url, t('mcp.connUrl'))}>{t('common.copy')}</Button></p>
          <p class="hint">{t('mcp.connExpect')} {t('mcp.connPublic')}</p>
          <div class="visually-hidden" aria-live="polite">{copied}</div>
        {:else}
          <p class="hint">{t('mcp.connOff')}</p>
        {/if}
        <div class="row"><Button onclick={close}>{t('common.done')}</Button></div>
      {:else}
      <fieldset class="plain-fieldset stack-sm">
        <legend class="label">{t('mcp.households')}</legend>
        {#each households as h (h.id)}
          <label class="check"><input type="checkbox" value={h.id} bind:group={picked} />{h.name}</label>
        {/each}
      </fieldset>
      <fieldset class="plain-fieldset stack-sm">
        <legend class="label">{t('mcp.may')}</legend>
        <p class="hint">{t('mcp.alwaysRead')}</p>
        <label class="check"><input type="checkbox" bind:checked={canComplete} />{t('mcp.mayComplete')}</label>
        <label class="check"><input type="checkbox" bind:checked={canWrite} />{t('mcp.mayWrite')}</label>
      </fieldset>
      <div aria-live="polite"><Alert message={err} /></div>
      <div class="row">
        <Button type="submit" {busy}>{t('mcp.create')}</Button>
        <Button variant="quiet" onclick={close}>{t('common.cancel')}</Button>
      </div>
      {/if}
    </form>
  {/if}
</Dialog>
