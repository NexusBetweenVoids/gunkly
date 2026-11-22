<script lang="ts">
  import { t } from '../lib/i18n';
  import Settings from './admin/Settings.svelte';
  import Updates from './admin/Updates.svelte';
  import Users from './admin/Users.svelte';
</script>

<h1>{t('admin.title')}</h1>
<div class="sections">
  <Users />
  <Settings />
  <Updates />
</div>
