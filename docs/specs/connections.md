# Connections — Phase 1 spec

Source: decoded Navicat Premium 18.0.3 connection-form screens (field labels, defaults, enable rules).
Describes observable behavior only. "Inferred" marks options that the app fills in at runtime, so they aren't visible in the screen files.

## New Connection flow
1. **Type picker** ("New Connection"/"Select a connection type:"): a searchable list of connection types with an icon view and a list view toggle.
2. **Connection dialog**: tabs that vary by DB type (below). Footer: **Test Connection**, Cancel, OK/Save.
3. **Copy to Clipboard** on the dialog copies the connection settings (popover variant).

### Tabs by DB type
| Tab | MySQL/MariaDB | PostgreSQL | MongoDB | Redis | SQLite |
|---|---|---|---|---|---|
| General | ✓ | ✓ | ✓ | ✓ | ✓ |
| Advanced | ✓ | ✓ | ✓ | ✓ | ✓ |
| Databases (custom list) | ✓ | ✓ | ✓ | – | Attached DBs |
| SSL | ✓ | ✓ | ✓ (Mongo variant) | ✓ | – |
| SSH | ✓ | ✓ | ✓ | ✓ | – |
| HTTP tunnel | ✓ | ✓ | – | – | ✓ (skip) |
| Sentinels | – | – | – | ✓ (Sentinel type) | – |
| Remarks | ✓ | ✓ | ✓ | ✓ | ✓ |
| Compatibility (button in Advanced) | MySQL | – | – | Redis | – |

## General tab
Common to all types: **Connection Name**, **Add To** (group/folder; default "My Connections"), **Save password** checkbox (password field enabled only when it's checked). Chloe: replace Add To with a local folder/group picker. There's no cloud.

| DB | Fields |
|---|---|
| MySQL / MariaDB | Host, Port (placeholder 3306), User Name, Password |
| PostgreSQL | **Node Type** (inferred: Single Node / Multiple Hosts). For multiple hosts: **Member** table (Host, Port, +/−) and **Host Type Preference** (inferred: Any / Primary / Standby / Prefer Standby). Otherwise Host, Port (5432). Then **Initial Database**, User Name, Password |
| MongoDB | **Type** (inferred: Standalone / Replica Set / Sharded Cluster), **SRV record** checkbox, Host, Port (27017). **Member** table (Host, Port, Status) with **Discover** for replica sets. **Read Preference**: Primary (default) / Primary Preferred / Secondary / Secondary Preferred / Nearest. **Replica Set** name. **Authentication**: None / SCRAM-SHA-1 / SCRAM-SHA-256 / MONGODB-CR / X.509 / Kerberos (GSSAPI) / LDAP (PLAIN). **Authentication Database**. Kerberos only: **Service Name**, **Principal**, **Use canonicalized host name**. User Name, Password |
| Redis | **Type**: Standalone / Cluster / Sentinel. Sentinel only: **Sentinel Host**, **Sentinel Port** (26379), **Sentinel Authentication** (None / Password / Username and Password), Sentinel User/Password + save, **Role** (Master / Replica), **Group Name** (combo, filled via Sentinel). Then Host, Port (6379), **Authentication** (None / Password / Username+Password ACL), User Name, Password |
| SQLite | **Type**: Existing Database File / New SQLite 3 (skip New SQLite 2). **Database File** path picker. Optional User/Password |

## Advanced tab
All types: **Settings Location** (where per-connection files live, read-only path), **Auto connect** (connect at startup), **Limit connection sessions** [10], **Keepalive interval (sec)** [240], **Initial Queries** (SQL run on every new session).

| DB | Extra fields (checkbox enables the field; default value in brackets) |
|---|---|
| MySQL / MariaDB | Client Character Set [Auto], Encoding, Connection Timeout (sec), Read Timeout, Write Timeout, **Use compression**, **Use socket file** [/tmp/mysql.sock], **Compatibility…** button |
| PostgreSQL | Client Character Set [Auto], Encoding, **Connection Parameters** (extra libpq-style `key=value`) |
| MongoDB | Connection Timeout (ms), Server Selection Timeout (ms), Socket Timeout (ms), **Connection Parameters** (URI options), **Retryable Reads**, **Retryable Writes** |
| Redis | Connection Timeout (ms), **Compatibility…** button |
| SQLite | **Encrypt** + Password + Save password, **Cipher Name** (sqlite3mc ciphers), **Cipher Legacy Page Size** |

### MySQL compatibility dialog
"Use Compatibility Mode" plus override checkboxes, each with a value:
- Force `lower_case_table_names` as {0,1,2}. When on, skip `SHOW VARIABLES LIKE 'lower_case_%'`.
- Force `sql_mode` as [text].
- Force NDB cluster support as {Yes/No}.
- Force database listing method (inferred: SHOW DATABASES vs information_schema).
- Force view listing method (inferred: SHOW FULL TABLES vs information_schema).

### Redis compatibility
"Use Compatibility Mode" plus "Force server version as" [x.y.z]. This gates which commands and features are offered.

## Databases tab (custom database list)
"Use custom database list" switches the tree to show only the listed DBs. Table columns: active ✓, **Name**, **Auto Open**. Buttons: +, −, Refresh (fetches from server).

## SSL tab (MySQL/PG/Redis)
- **Use SSL**
- PG only: **SSL Mode**: prefer (default) / allow / require / verify-ca / verify-full (add `disable` implicitly when SSL is off).
- **Use authentication** → Client Key, Client Certificate, CA Certificate (file pickers).
- MySQL: **Verify server certificate against CA**, **Specified Cipher**.
- PG: **Root Certificate**, **Certificate Revocation List**.

### MongoDB SSL
Use SSL, Use authentication → Client Key (PEM) + Client Key Password (+save), **Allow any server SSL certificates**, CA Certificate, Certificate Revocation List, **Allow invalid host names**, Subject Name.

## SSH tab
- **Use SSH tunnel** (enables all fields)
- Host, Port [22], User Name
- **Authentication Method**: Password (default) / Public Key / Password and Public Key
- Password (+Save password), Private Key (path), Passphrase (+Save passphrase)
- Host keys: checked against `~/.ssh/known_hosts` and Chloe's own `known_hosts` in the config directory. A host
  in neither is trusted on first use and its key recorded in Chloe's file; a different key later fails the
  connection with the new fingerprint and the file and line to delete if the change was intended.
- Chloe additions to consider: `~/.ssh/config` host aliases, a fingerprint prompt on first connect.

## HTTP tunnel (low priority)
Use HTTP tunnel, Tunnel URL, Encode outgoing query with base64, Use authentication (User/Password/save), Proxy Settings…, Export Tunnel Script…. This needs server-side PHP scripts, so we defer it.

## Remarks tab
Free-text "Remarks" on the connection.

## Password handling
- Passwords live in the OS keychain (callbacks `KeyChainGet/Set/Remove` per workspace connection). Chloe: macOS Keychain, Windows Credential Manager via `zalando/go-keyring`.
- If a password isn't saved, a **"Connection Ask Password"** prompt appears on connect.

## Connection tree behaviors related to connections
- Open/close connection; connection color/grouping; **Flatten Connection** view option.
- Import Connections… / Export Connections… (File menu). Navicat exports `.ncx` XML. Chloe: our own JSON format, plus optional `.ncx` import.
