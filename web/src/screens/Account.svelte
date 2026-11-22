<script lang="ts">
  import Channels from '../components/Channels.svelte';
  import Deliveries from '../components/Deliveries.svelte';
  import Lazy from '../components/Lazy.svelte';
  import { t } from '../lib/i18n';
  import Audit from './account/Audit.svelte';
  import Email from './account/Email.svelte';
  import EmailCodes from './account/EmailCodes.svelte';
  import Password from './account/Password.svelte';
  import PushNotifications from './account/PushNotifications.svelte';
  import Profile from './account/Profile.svelte';
  import Sessions from './account/Sessions.svelte';
  import Totp from './account/Totp.svelte';
</script>

<h1>{t('nav.account')}</h1>
<div class="sections">
  <Profile />
  <Email />
  <Password />
  <Totp />
  <EmailCodes />
  <Channels base="/account/channels" title={t('channels.personalTitle')} id="me" />
  <PushNotifications />
  <Deliveries path="/account/deliveries" title={t('delivery.personalTitle')} id="me" />
  <Sessions />
  <Lazy load={() => import('./account/ExportData.svelte')} />
  <Audit />
</div>
