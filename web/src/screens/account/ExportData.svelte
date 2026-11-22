<script lang="ts">
  import Alert from '../../components/Alert.svelte';
  import Button from '../../components/Button.svelte';
  import { download } from '../../lib/api';
  import { failure } from '../../lib/errors';
  import { t } from '../../lib/i18n';
  import { withStepUp } from '../../lib/stepup.svelte';
  import { toast } from '../../lib/toast.svelte';

  let busy = $state(false);
  let err = $state('');

  async function run(): Promise<void> {
    err = '';
    busy = true;
    try {
      await withStepUp(() => download('/account/export', 'gunkly-user-export.json'));
      toast(t('export.downloaded'));
    } catch (x) {
      err = failure(x).message;
    } finally {
      busy = false;
    }
  }
</script>

<section class="card" aria-labelledby="a-export">
  <h2 id="a-export">{t('export.accountTitle')}</h2>
  <p class="muted">{t('export.accountIntro')}</p>
  <div aria-live="polite"><Alert message={err} /></div>
  <div class="mt-md"><Button onclick={run} {busy}>{t('export.accountButton')}</Button></div>
</section>
