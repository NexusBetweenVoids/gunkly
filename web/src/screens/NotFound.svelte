<script lang="ts">
  import { href } from '../lib/base';
  import { t } from '../lib/i18n';
</script>

<h1>{t('notFound.title')}</h1>
<p><a class="btn btn-quiet" href={href('/')}>{t('notFound.back')}</a></p>
