// Regression check for src/lib/qr.ts: fixed inputs must produce the exact module matrices that
// python-qrcode (the reference) produced once, forcing the version and mask this encoder picks, plus basic invariants. Runs under plain Node
// (type stripping), no build step. Snapshot = rows as hex, one string per matrix, LSB-padded.
import { qrEncode } from '../src/lib/qr.ts';

const hex = (m) => {
  const bits = m.flat().map(Number).join('');
  let h = '';
  for (let i = 0; i < bits.length; i += 4) h += parseInt(bits.slice(i, i + 4).padEnd(4, '0'), 2).toString(16);
  return h;
};
const snaps = [
  { level: 'M', version: 1, text: "hello", hex: "fe63fc16106e96bb7465dbacaec10507faafe00700aa50916110d4ba3f900426b2a8007a9ff8dcf047b0bab71dd219aeba2b04852feeb18" },
  { level: 'H', version: 1, text: "hello", hex: "fe0bfc10106eb4bb7545dba3aec11107faafe00e001b70652b1f35b54f9a9b44f83d00572bfa88904e8fbaa8ddd5e42e8dff04ffffe2400" },
  { level: 'M', version: 7, text: "otpauth://totp/Gunkly:sam?secret=JBSWY3DPEHPK3PXPJBSWY3DPEHPK3PXP&issuer=Gunkly&algorithm=SHA1&digits=6&period=30", hex: "fe5af4420bfc132a1d80906eaefa6474bb7506d9cab5dbae87f9b3aec15b547e8107faaaaaaaafe017ef1f9500be1caf8123e445b7b67ae3e8fce85e5f8d1935b014337ef5c6b314e5d693ae230e1ead8ce7205689b7148fb17e677b708504640f91daf06da3992828c99d0839ab25487bfc7ff851f86472144b2c7deb916a58ead5121117351ebfc0cfc52fd6217dd0fc327baef21a497c31d96b12a6e8213897744a6049106b0c03781c270310c839389bfb6c476ee1f565eb6dbff63e73c2cf38cf6d40f1cc0fc738f9b315fa36f88052247b6457f81eaa7daad05f0b17811dbadbafa35f85d59c1a388ceead79597fc504e4f7b83acfebb73e50730" },
  { level: 'H', version: 9, text: "otpauth://totp/Gunkly:sam?secret=JBSWY3DPEHPK3PXPJBSWY3DPEHPK3PXP&issuer=Gunkly&digits=6&period=30", hex: "fe81dcec5923fc1757a892f3906eb9c9b7fa24bb749af51a10d5dba693cf80d62ec1537547445107faaaaaaaaaafe0137e717ab6003aa1cef9b95f3868b78d416f80e2e19ac4ef75d811d9d7d16363823e124e86e3fd5c2fa3030278572d057e943aca129a947b137e9997d4f649bc5dcc80a8c8386b81d59cd071689999f2081634e9f63df1cd928e80af10b7c017b870e72cee59103c11d1981b87dee0a97fa9cdfcfe7ff74539e449c54796aab4aa48c6b1f1ca7316e45104fa0cbfb7c1fed23b28d76cb9fb0a99017a7e2586b42aa77dddeac2529948bc2472ccd3f9cde356368d98f5be00205b65dc0677b9a470a71ef3e0d714332e037877f06b8ba511be230c247aa8b917332bb02344f90e77ae0e7e972be6f7950e67106218c20b3677abf391377d4f93c6fd005e2345023c4ff83786ac8eaa10470f519ab11abaa7b4f83e6f85d79f5561ad70eea7d93e3494f904c63ea4ed03afe4897480c67c0" },
  { level: 'M', version: 8, text: "Grüße, 世界 — Gunkly 🔐 Grüße, 世界 — Gunkly 🔐 Grüße, 世界 — Gunkly 🔐 Grüße, 世界 — Gunkly 🔐 ", hex: "fe3b008358bfc1026a6b05d06ea442e7f56bb758fe52ea25dbacfc7e7ac2ec168c910b7107faaaaaaaaafe010904572000be6da7e3983e56a364d40bfc3fefebb19ec2b300407178a8f1b9986294cc9ace4fd7af636acaa1384c4a1740a537e85ce2c3574c09f78dd0829c8e51a23aae90aaf543a4a62c0f885659f353aa169ad9ab4f4773d4d23faa9fa117f4910b646953187acee6a47bafac48ef1885c40bfe10fdba3e71a2a79345dce5a3b92f5224d0a0df1b03e987988250f06dae83710e869bb1c341cd16096ab0115331a5beeaf7792d8348932e73fc1aa0d837f689c5afac2931c8737e6d1f469bc04c10e1ac027fc0e5e26dfbf329fe0070671805c5ff958fadff6b105d94c59351aba89a7e246fedd6ebfca3af06ea43212c6ce104b3f1284041fede27c9e99f8" },
];
let bad = 0;
for (const s of snaps) {
  const m = qrEncode(s.text, s.level);
  const got = hex(m);
  if ((m.length - 17) / 4 !== s.version || got !== s.hex) {
    console.error(`qr-check: mismatch for ${s.level} ${JSON.stringify(s.text.slice(0, 40))}`);
    bad++;
  }
}
try {
  qrEncode('x'.repeat(1000), 'M');
  console.error('qr-check: over-long input did not throw');
  bad++;
} catch {}
if (bad) process.exit(1);
console.log(`qr-check: ${snaps.length} snapshots ok`);
