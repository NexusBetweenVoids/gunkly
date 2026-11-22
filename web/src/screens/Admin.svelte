<script lang="ts">
  import { t } from '../lib/i18n';
  import Settings from './admin/Settings.svelte';
  import Users from './admin/Users.svelte';
</script>

<h1>{t('admin.title')}</h1>
<p><a href="/admin/updates">{t('nav.updates')}</a></p>
<div class="sections">
  <Users />
  <Settings />
</div>
