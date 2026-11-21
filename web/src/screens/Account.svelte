<script lang="ts">
  import { t } from '../lib/i18n';
  import Audit from './account/Audit.svelte';
  import Password from './account/Password.svelte';
  import Profile from './account/Profile.svelte';
  import Sessions from './account/Sessions.svelte';
  import Totp from './account/Totp.svelte';
</script>

<h1>{t('account.title')}</h1>
<div class="sections">
  <Profile />
  <Password />
  <Totp />
  <Sessions />
  <Audit />
</div>
