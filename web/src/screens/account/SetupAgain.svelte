<script lang="ts">
  import { href } from '../../lib/base';
  // Go through the welcome flow again (household, starter chores, invite, security).
  import { t } from '../../lib/i18n';
</script>

<section class="card stack" aria-labelledby="setup-again-title">
  <h2 id="setup-again-title">{t('setupAgain.title')}</h2>
  <p class="hint">{t('setupAgain.hint')}</p>
  <div><a class="btn btn-quiet" href={href('/welcome')}>{t('setupAgain.start')}</a></div>
</section>
