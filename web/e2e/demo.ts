import { type World, ymd } from "./helpers";

// Synthetic demo data for screenshots (README, website, design review): Alex and Sam share
// "Maple Street 12" with areas and ten chores, some overdue, some taking turns.
// `names`: usernames "alex" and "sam" (only on a fresh server, as in the screenshot run).
export async function seedDemo(
  world: World,
  opts: { admin?: boolean; names?: boolean } = {},
) {
  const alex = await world.newUser({
    admin: opts.admin,
    ...(opts.names ? { username: "alex" } : {}),
  });
  await alex.api.call("PATCH", "/me", { displayName: "Alex" });
  const sam = await world.newUser(opts.names ? { username: "sam" } : {});
  await sam.api.call("PATCH", "/me", { displayName: "Sam" });
  const h = await world.household(alex, "Maple Street 12", { areas: true });
  const inv = await alex.api.post<{ token: string }>(
    `/households/${h.id}/invites`,
    { role: "member", expiresInHours: 24 },
  );
  await sam.api.post(`/invites/${inv.token}/accept`);
  await alex.api.post(`/households/${h.id}/invites`, {
    role: "guest",
    expiresInHours: 72,
  });
  const members = await alex.api.get<{ userId: string; displayName: string }[]>(
    `/households/${h.id}/members`,
  );
  const A = members.find((m) => m.displayName === "Alex")!.userId;
  const S = members.find((m) => m.displayName === "Sam")!.userId;
  const areas = await alex.api.get<{ id: string; name: string }[]>(
    `/households/${h.id}/areas`,
  );
  const area = (n: string) => areas.find((a) => a.name === n)?.id ?? null;
  const iv = (every: number, unit: string, due: number) => ({
    type: "interval",
    interval: { every, unit },
    anchor: "from_completion",
    firstDue: ymd(due),
  });
  const chores: [string, string, string[], ReturnType<typeof iv>, boolean?][] =
    [
      ["Descale the kettle", "Kitchen", [A], iv(1, "month", -3)],
      ["Washing-machine filter", "Appliances", [S], iv(3, "month", -1)],
      ["Wash the bedding", "Bathroom", [S], iv(2, "week", -1)],
      ["Clean the gutters", "Exterior", [A, S], iv(6, "month", 0), true],
      ["Water the plants", "Garden", [S], iv(4, "day", 0)],
      ["Defrost the freezer", "Kitchen", [A], iv(6, "month", 0)],
      ["Take out the recycling", "General", [A, S], iv(1, "week", 3), true],
      ["Vacuum the stairs", "General", [A], iv(1, "week", 5)],
      ["Clean the oven", "Kitchen", [S], iv(2, "month", 8)],
      ["Test smoke alarms", "General", [A], iv(6, "month", 12)],
    ];
  let gutters = "";
  for (const [name, a, who, schedule, rotation] of chores) {
    const c = await world.chore(alex, h, {
      name,
      areaId: area(a),
      assignees: who,
      schedule,
      ...(rotation ? { rotation: true } : {}),
    });
    if (name === "Clean the gutters") gutters = c.id;
  }

  return { alex, sam, h, gutters };
}
