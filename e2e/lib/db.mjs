// Ports of the Docker test databases as the connection dialog takes them. CHLOE_DB_SLOT=N
// picks the set started with the same variable: every port moves up by N*10 (testdata/slot.sh).
const slot = Number(process.env.CHLOE_DB_SLOT || 0);
const port = (base) => String(base + slot * 10);
export const MYSQL = port(33306), PG = port(55432), REDIS = port(63790), MONGO = port(37017), SSH = port(2222);
