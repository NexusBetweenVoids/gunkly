<script lang="ts">
  import { href } from '../lib/base';
  let { size = 28 }: { size?: number } = $props();
</script>

<img class="logo-light" src={href('/icons/mark-light.png')} alt="" width={size} height={size} />
<img class="logo-dark" src={href('/icons/mark-dark.png')} alt="" width={size} height={size} />
