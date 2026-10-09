// Package legacy imports data from the old Kannel/PHP gateway's MySQL database into the new PostgreSQL
// database. Every step is idempotent (keyed by legacy ids or deterministic message ids), so the import can be
// run once to prepare and again right before cutover to bring over what changed.
package legacy

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"math"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/Adnan-Dogar/telivoz-gateway/internal/auth"
	_ "github.com/go-sql-driver/mysql"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// messageNamespace makes legacy message ids deterministic, so re-importing never duplicates history.
var messageNamespace = uuid.MustParse("6f1d7c2e-6b0a-4c43-9d4e-4f5b8c0c2a11")

type importer struct {
	my     *sql.DB
	pg     *pgxpool.Pool
	cipher *auth.Cipher
	log    *slog.Logger
	report []string

	countries   map[string]bool
	networks    map[string]int64 // "mcc-mnc" -> id
	users       map[int64]int64
	clients     map[int64]int64
	smppAccts   map[int64]int64
	connections map[int64]int64

	placeholders int
}

// Import copies the legacy data. mysqlDSN uses the go-sql-driver format: user:pass@tcp(host:3306)/dbname
func Import(ctx context.Context, pg *pgxpool.Pool, cipher *auth.Cipher, mysqlDSN string, log *slog.Logger) error {
	if !strings.Contains(mysqlDSN, "?") {
		mysqlDSN += "?"
	} else {
		mysqlDSN += "&"
	}
	mysqlDSN += "parseTime=true&charset=utf8mb4&loc=UTC"
	my, err := sql.Open("mysql", mysqlDSN)
	if err != nil {
		return err
	}
	defer my.Close()
	if err := my.PingContext(ctx); err != nil {
		return fmt.Errorf("connect to legacy MySQL: %w", err)
	}
	im := &importer{my: my, pg: pg, cipher: cipher, log: log, countries: map[string]bool{}, networks: map[string]int64{},
		users: map[int64]int64{}, clients: map[int64]int64{}, smppAccts: map[int64]int64{}, connections: map[int64]int64{}}
	steps := []struct {
		name string
		fn   func(context.Context) error
	}{
		{"countries", im.importCountries},
		{"networks and prefixes", im.importNetworks},
		{"staff users", im.importUsers},
		{"clients and balances", im.importClients},
		{"client logins", im.linkClientUsers},
		{"SMPP and HTTP accounts", im.importAccounts},
		{"vendors and connections", im.importVendors},
		{"client rates", im.importClientRates},
		{"vendor rates", im.importVendorRates},
		{"routes", im.importRoutes},
		{"message history", im.importMessages},
		{"daily statistics", im.importStats},
	}
	for _, st := range steps {
		start := time.Now()
		log.Info("import step", "step", st.name)
		if err := st.fn(ctx); err != nil {
			return fmt.Errorf("%s: %w", st.name, err)
		}
		log.Info("import step done", "step", st.name, "took", time.Since(start).Round(time.Millisecond))
	}
	fmt.Println("\n=== Legacy import report ===")
	for _, line := range im.report {
		fmt.Println(line)
	}
	return nil
}

func (im *importer) note(format string, args ...any) {
	im.report = append(im.report, fmt.Sprintf(format, args...))
}

func (im *importer) tableExists(ctx context.Context, name string) bool {
	var n int
	_ = im.my.QueryRowContext(ctx, `SELECT COUNT(*) FROM information_schema.tables WHERE table_schema = DATABASE() AND table_name = ?`, name).Scan(&n)
	return n > 0
}

func upperISO(s string) string {
	s = strings.ToUpper(strings.TrimSpace(s))
	if len(s) != 2 || s == "00" {
		return ""
	}
	return s
}

func money(f float64) string { return strconv.FormatFloat(math.Round(f*1e6)/1e6, 'f', 6, 64) }

func nullable(id int64) any {
	if id == 0 {
		return nil
	}
	return id
}

func (im *importer) iso(s string) any {
	s = upperISO(s)
	if s == "" || !im.countries[s] {
		return nil
	}
	return s
}

// ---- reference data --------------------------------------------------------------------------------------

func (im *importer) importCountries(ctx context.Context) error {
	rows, err := im.my.QueryContext(ctx, `SELECT iso, COALESCE(NULLIF(nicename, ''), name), COALESCE(phonecode, 0) FROM countries`)
	if err != nil {
		return err
	}
	defer rows.Close()
	n := 0
	for rows.Next() {
		var iso, name string
		var code int
		if err := rows.Scan(&iso, &name, &code); err != nil {
			return err
		}
		if iso = upperISO(iso); iso == "" {
			continue
		}
		dial := ""
		if code > 0 {
			dial = strconv.Itoa(code)
		}
		if _, err := im.pg.Exec(ctx, `INSERT INTO countries (iso, name, dial_code) VALUES ($1, $2, $3)
			ON CONFLICT (iso) DO UPDATE SET name = EXCLUDED.name, dial_code = EXCLUDED.dial_code`, iso, name, dial); err != nil {
			return err
		}
		im.countries[iso] = true
		n++
	}
	im.note("countries:          %d", n)
	return rows.Err()
}

func (im *importer) importNetworks(ctx context.Context) error {
	// Country per MCC, learned from the legacy networks table.
	mccCountry := map[string]string{}
	type net struct{ iso, name string }
	nets := map[string]net{}
	rows, err := im.my.QueryContext(ctx, `SELECT mcc, mnc, COALESCE(iso, ''), COALESCE(network, '') FROM networks`)
	if err != nil {
		return err
	}
	for rows.Next() {
		var mcc, mnc, iso, name string
		if err := rows.Scan(&mcc, &mnc, &iso, &name); err != nil {
			rows.Close()
			return err
		}
		mcc, mnc = strings.TrimSpace(mcc), strings.TrimSpace(mnc)
		if mcc == "" || mnc == "" {
			continue
		}
		if c := upperISO(iso); c != "" {
			mccCountry[mcc] = c
		}
		nets[mcc+"-"+mnc] = net{upperISO(iso), name}
	}
	rows.Close()

	// Prefixes (with the country code included), plus networks only known from the prefix table.
	type prefix struct{ prefix, key string }
	var prefixes []prefix
	rows, err = im.my.QueryContext(ctx, `SELECT CAST(prefix AS CHAR), mcc, mnc, COALESCE(network_name, ''), COALESCE(iso, '') FROM network_prefixes`)
	if err != nil {
		return err
	}
	for rows.Next() {
		var p, mcc, mnc, name, iso string
		if err := rows.Scan(&p, &mcc, &mnc, &name, &iso); err != nil {
			rows.Close()
			return err
		}
		mcc, mnc = strings.TrimSpace(mcc), strings.TrimSpace(mnc)
		if p == "" || mcc == "" || mnc == "" {
			continue
		}
		key := mcc + "-" + mnc
		if _, ok := nets[key]; !ok {
			nets[key] = net{upperISO(iso), name}
		}
		prefixes = append(prefixes, prefix{p, key})
	}
	rows.Close()

	for key, n := range nets {
		mcc, mnc, _ := strings.Cut(key, "-")
		iso := n.iso
		if iso == "" {
			iso = mccCountry[mcc]
		}
		var id int64
		if err := im.pg.QueryRow(ctx, `INSERT INTO networks (country_iso, mcc, mnc, name) VALUES ($1, $2, $3, $4)
			ON CONFLICT (mcc, mnc) DO UPDATE SET name = CASE WHEN EXCLUDED.name <> '' THEN EXCLUDED.name ELSE networks.name END,
				country_iso = COALESCE(EXCLUDED.country_iso, networks.country_iso)
			RETURNING id`, im.iso(iso), mcc, mnc, n.name).Scan(&id); err != nil {
			return err
		}
		im.networks[key] = id
	}

	batch := &pgx.Batch{}
	for _, p := range prefixes {
		batch.Queue(`INSERT INTO number_prefixes (prefix, network_id) VALUES ($1, $2)
			ON CONFLICT (prefix) DO UPDATE SET network_id = EXCLUDED.network_id`, p.prefix, im.networks[p.key])
	}
	if err := im.pg.SendBatch(ctx, batch).Close(); err != nil {
		return err
	}
	im.note("networks:           %d", len(nets))
	im.note("number prefixes:    %d", len(prefixes))
	return nil
}

// ---- people ----------------------------------------------------------------------------------------------

func (im *importer) importUsers(ctx context.Context) error {
	rows, err := im.my.QueryContext(ctx, `SELECT u.id, COALESCE(u.email_address, ''), u.user_name,
			TRIM(CONCAT(COALESCE(u.first_name, ''), ' ', COALESCE(u.last_name, ''))), COALESCE(u.password, ''), u.status,
			COALESCE(r.is_super_admin, 0), COALESCE(r.client_id, 0), COALESCE(u.client_id, 0), COALESCE(u.is_client, 0)
		FROM users u LEFT JOIN roles r ON r.id = u.role_id`)
	if err != nil {
		return err
	}
	defer rows.Close()
	staff, clientsUsers := 0, 0
	for rows.Next() {
		var id, status, superAdmin, roleClient, clientID, isClient int64
		var email, username, name, hash string
		if err := rows.Scan(&id, &email, &username, &name, &hash, &status, &superAdmin, &roleClient, &clientID, &isClient); err != nil {
			return err
		}
		role := "sales"
		switch {
		case isClient == 1 || clientID > 0 || roleClient > 0:
			role = "client"
		case superAdmin == 1:
			role = "admin"
		}
		email = strings.ToLower(strings.TrimSpace(email))
		if email == "" {
			email = strings.ToLower(username) + "@legacy.invalid"
		}
		st := "disabled"
		if status == 10 {
			st = "active"
		}
		if role == "client" && clientID == 0 {
			clientID = roleClient
		}
		var newID int64
		// Client users are created without client_id here and linked after clients exist.
		if err := im.pg.QueryRow(ctx, `INSERT INTO users (email, name, password_hash, role, status, legacy_id)
			VALUES ($1, $2, $3, $4, $5, $6)
			ON CONFLICT (legacy_id) DO UPDATE SET email = EXCLUDED.email, name = EXCLUDED.name,
				password_hash = EXCLUDED.password_hash, role = EXCLUDED.role, status = EXCLUDED.status
			RETURNING id`, email, strings.TrimSpace(name), hash, map[bool]string{true: "sales", false: role}[role == "client"], st, id).Scan(&newID); err != nil {
			return fmt.Errorf("user %d (%s): %w", id, email, err)
		}
		im.users[id] = newID
		if role == "client" {
			clientsUsers++
			pendingClientUsers = append(pendingClientUsers, [2]int64{newID, clientID})
		} else {
			staff++
		}
	}
	im.note("users:              %d staff, %d client logins (same passwords as before)", staff, clientsUsers)
	return rows.Err()
}

var pendingClientUsers [][2]int64

func (im *importer) importClients(ctx context.Context) error {
	currencies := map[int64]string{}
	if rows, err := im.my.QueryContext(ctx, `SELECT id, code FROM currencies`); err == nil {
		for rows.Next() {
			var id int64
			var code string
			if rows.Scan(&id, &code) == nil {
				currencies[id] = strings.ToUpper(strings.TrimSpace(code))
			}
		}
		rows.Close()
	}
	countryISO := map[int64]string{}
	if rows, err := im.my.QueryContext(ctx, `SELECT id, iso FROM countries`); err == nil {
		for rows.Next() {
			var id int64
			var iso string
			if rows.Scan(&id, &iso) == nil {
				countryISO[id] = upperISO(iso)
			}
		}
		rows.Close()
	}
	rows, err := im.my.QueryContext(ctx, `SELECT c.id, c.client_name, COALESCE(c.email_address, ''), COALESCE(c.primary_phone, ''),
			COALESCE(c.country_id, 0), COALESCE(c.billing_currency_id, 0), c.status, c.created_by, COALESCE(c.parent_client_id, 0),
			COALESCE(cs.field3, ''), COALESCE(cs.billing_type, 'money'), COALESCE(b.balance, 0)
		FROM clients c
		LEFT JOIN (SELECT client_id, MAX(field3) AS field3, MAX(billing_type) AS billing_type FROM company_settings GROUP BY client_id) cs ON cs.client_id = c.id
		LEFT JOIN (SELECT client_id, SUM(balance) AS balance FROM sms_credits_balances GROUP BY client_id) b ON b.client_id = c.id
		ORDER BY c.id`)
	if err != nil {
		return err
	}
	defer rows.Close()
	type parent struct{ child, parent int64 }
	var parents []parent
	n := 0
	var total float64
	for rows.Next() {
		var id, countryID, currencyID, status, createdBy, parentID int64
		var name, email, phone, webhook, billing string
		var balance float64
		if err := rows.Scan(&id, &name, &email, &phone, &countryID, &currencyID, &status, &createdBy, &parentID, &webhook, &billing, &balance); err != nil {
			return err
		}
		currency := currencies[currencyID]
		if len(currency) != 3 {
			currency = "USD"
		}
		st := "disabled"
		if status == 1 {
			st = "active"
		}
		owner := im.users[createdBy]
		dlrFormat := "v1"
		if strings.HasPrefix(webhook, "http") {
			dlrFormat = "legacy" // keep the old DLR payload for clients already integrated
		} else {
			webhook = ""
		}
		var newID int64
		if err := im.pg.QueryRow(ctx, `INSERT INTO clients (name, email, phone, country_iso, currency, owner_id, dlr_webhook_url,
				dlr_format, status, legacy_id)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
			ON CONFLICT (legacy_id) DO UPDATE SET name = EXCLUDED.name, email = EXCLUDED.email, phone = EXCLUDED.phone,
				country_iso = EXCLUDED.country_iso, currency = EXCLUDED.currency, dlr_webhook_url = EXCLUDED.dlr_webhook_url,
				dlr_format = EXCLUDED.dlr_format, status = EXCLUDED.status,
				owner_id = COALESCE(clients.owner_id, EXCLUDED.owner_id)
			RETURNING id`, name, email, phone, im.iso(countryISO[countryID]), currency, nullable(owner), webhook, dlrFormat, st, id).Scan(&newID); err != nil {
			return fmt.Errorf("client %d: %w", id, err)
		}
		im.clients[id] = newID
		if parentID > 0 {
			parents = append(parents, parent{newID, parentID})
		}
		// The balance is replaced by the legacy value each run (until cutover the old system is the source).
		bal := money(balance)
		if err := pgx.BeginFunc(ctx, im.pg, func(tx pgx.Tx) error {
			if _, err := tx.Exec(ctx, `INSERT INTO balances (client_id, balance) VALUES ($1, $2::numeric)
				ON CONFLICT (client_id) DO UPDATE SET balance = EXCLUDED.balance, updated_at = now()`, newID, bal); err != nil {
				return err
			}
			if _, err := tx.Exec(ctx, `DELETE FROM ledger WHERE client_id = $1 AND kind = 'migration'`, newID); err != nil {
				return err
			}
			_, err := tx.Exec(ctx, `INSERT INTO ledger (client_id, kind, amount, balance_after, note)
				VALUES ($1, 'migration', $2::numeric, $2::numeric, 'balance imported from the legacy system')`, newID, bal)
			return err
		}); err != nil {
			return err
		}
		total += balance
		n++
	}
	for _, p := range parents {
		if pid := im.clients[p.parent]; pid != 0 {
			_, _ = im.pg.Exec(ctx, `UPDATE clients SET parent_id = $2 WHERE id = $1`, p.child, pid)
		}
	}
	im.note("clients:            %d (total balance %s, rounded to 6 decimals)", n, money(total))
	return rows.Err()
}

// historyClient returns the new id for a legacy client referenced by history. Clients deleted from the old
// system get a disabled placeholder, so their traffic and revenue still count in reports.
func (im *importer) historyClient(ctx context.Context, legacyID int64) int64 {
	if id := im.clients[legacyID]; id != 0 {
		return id
	}
	var id int64
	err := im.pg.QueryRow(ctx, `INSERT INTO clients (name, status, legacy_id) VALUES ($1, 'disabled', $2)
		ON CONFLICT (legacy_id) DO UPDATE SET name = clients.name RETURNING id`,
		fmt.Sprintf("Legacy client #%d (deleted)", legacyID), legacyID).Scan(&id)
	if err != nil {
		im.log.Warn("placeholder client failed", "legacy_id", legacyID, "err", err)
		return 0
	}
	_, _ = im.pg.Exec(ctx, `INSERT INTO balances (client_id) VALUES ($1) ON CONFLICT DO NOTHING`, id)
	im.clients[legacyID] = id
	im.placeholders++
	return id
}

func (im *importer) linkClientUsers(ctx context.Context) error {
	linked := 0
	for _, pair := range pendingClientUsers {
		if cid := im.clients[pair[1]]; cid != 0 {
			if _, err := im.pg.Exec(ctx, `UPDATE users SET role = 'client', client_id = $2 WHERE id = $1`, pair[0], cid); err != nil {
				return err
			}
			linked++
		}
	}
	if missing := len(pendingClientUsers) - linked; missing > 0 {
		im.note("WARNING: %d client logins point to missing clients and stay as staff (sales); review them", missing)
	}
	pendingClientUsers = nil
	return nil
}

func (im *importer) importAccounts(ctx context.Context) error {
	rows, err := im.my.QueryContext(ctx, `SELECT id, client_id, smpp_username, smpp_password, COALESCE(allowed_ips, ''),
		status, COALESCE(tps, 0) FROM smpp_users`)
	if err != nil {
		return err
	}
	smpp := 0
	for rows.Next() {
		var id, clientID, status, tps int64
		var user, pass, ips string
		if err := rows.Scan(&id, &clientID, &user, &pass, &ips, &status, &tps); err != nil {
			rows.Close()
			return err
		}
		cid := im.clients[clientID]
		if cid == 0 {
			im.note("WARNING: SMPP user %q skipped: client %d not found", user, clientID)
			continue
		}
		hash, _ := auth.HashPassword(pass)
		var allowed []string
		for _, ip := range strings.FieldsFunc(ips, func(r rune) bool { return r == ',' || r == ';' || r == ' ' }) {
			allowed = append(allowed, ip)
		}
		if allowed == nil {
			allowed = []string{}
		}
		if tps <= 0 {
			tps = 100
		}
		var newID int64
		if err := im.pg.QueryRow(ctx, `INSERT INTO accounts (client_id, kind, username, password_hash, allowed_ips, tps, status, legacy_id)
			VALUES ($1, 'smpp', $2, $3, $4, $5, $6, $7)
			ON CONFLICT (legacy_id) DO UPDATE SET username = EXCLUDED.username, password_hash = EXCLUDED.password_hash,
				allowed_ips = EXCLUDED.allowed_ips, tps = EXCLUDED.tps, status = EXCLUDED.status
			RETURNING id`, cid, user, hash, allowed, tps, map[bool]string{true: "active", false: "disabled"}[status == 1],
			"smpp:"+strconv.FormatInt(id, 10)).Scan(&newID); err != nil {
			rows.Close()
			return fmt.Errorf("smpp user %q: %w", user, err)
		}
		im.smppAccts[id] = newID
		smpp++
	}
	rows.Close()

	rows, err = im.my.QueryContext(ctx, `SELECT id, client_id, user_name, password, status FROM api_credentials`)
	if err != nil {
		return err
	}
	defer rows.Close()
	httpN := 0
	for rows.Next() {
		var id, clientID, status int64
		var user, pass string
		if err := rows.Scan(&id, &clientID, &user, &pass, &status); err != nil {
			return err
		}
		cid := im.clients[clientID]
		if cid == 0 {
			continue
		}
		// HTTP clients send this stored value as their password; keep accepting it on /api/legacy.
		hash, _ := auth.HashPassword(pass)
		if _, err := im.pg.Exec(ctx, `INSERT INTO accounts (client_id, kind, username, password_hash, status, legacy_id)
			VALUES ($1, 'http', $2, $3, $4, $5)
			ON CONFLICT (legacy_id) DO UPDATE SET username = EXCLUDED.username, password_hash = EXCLUDED.password_hash, status = EXCLUDED.status`,
			cid, user, hash, map[bool]string{true: "active", false: "disabled"}[status == 1], "api:"+strconv.FormatInt(id, 10)); err != nil {
			return fmt.Errorf("api user %q: %w", user, err)
		}
		httpN++
	}
	im.note("accounts:           %d SMPP, %d HTTP (same usernames and passwords)", smpp, httpN)
	return rows.Err()
}

// ---- vendors, rates, routes ------------------------------------------------------------------------------

func (im *importer) importVendors(ctx context.Context) error {
	vendors := map[int64]int64{}
	rows, err := im.my.QueryContext(ctx, `SELECT id, vendor_name, COALESCE(email_address, ''), status, created_by FROM vendors`)
	if err != nil {
		return err
	}
	for rows.Next() {
		var id, status, createdBy int64
		var name, email string
		if err := rows.Scan(&id, &name, &email, &status, &createdBy); err != nil {
			rows.Close()
			return err
		}
		var newID int64
		if err := im.pg.QueryRow(ctx, `INSERT INTO vendors (name, email, owner_id, status, legacy_id) VALUES ($1, $2, $3, $4, $5)
			ON CONFLICT (legacy_id) DO UPDATE SET name = EXCLUDED.name, email = EXCLUDED.email, status = EXCLUDED.status
			RETURNING id`, name, email, nullable(im.users[createdBy]), map[bool]string{true: "active", false: "disabled"}[status == 1], id).Scan(&newID); err != nil {
			rows.Close()
			return err
		}
		vendors[id] = newID
	}
	rows.Close()

	rows, err = im.my.QueryContext(ctx, `SELECT id, COALESCE(vendor_id, 0), COALESCE(NULLIF(smsc_name, ''), gateway_name), gateway_type,
			COALESCE(host, ''), COALESCE(port, 0), COALESCE(username, ''), COALESCE(password, ''), COALESCE(system_type, ''),
			COALESCE(transceiver, 'transceiver'), COALESCE(field1, 0), COALESCE(max_pending_submits, 0), COALESCE(no_of_binds, 1),
			COALESCE(source_ton, 5), COALESCE(source_npi, 0), COALESCE(destination_ton, 1), COALESCE(destination_npi, 1), status
		FROM sms_gateways`)
	if err != nil {
		return err
	}
	defer rows.Close()
	n, skipped := 0, 0
	for rows.Next() {
		var id, vendorID, gtype, port, tps, window, binds, sTon, sNpi, dTon, dNpi, status int64
		var name, host, user, pass, sysType, mode string
		if err := rows.Scan(&id, &vendorID, &name, &gtype, &host, &port, &user, &pass, &sysType, &mode, &tps, &window, &binds,
			&sTon, &sNpi, &dTon, &dNpi, &status); err != nil {
			return err
		}
		if gtype != 2 || host == "" || port == 0 {
			skipped++
			continue
		}
		vid := vendors[vendorID]
		if vid == 0 {
			if err := im.pg.QueryRow(ctx, `INSERT INTO vendors (name, legacy_id) VALUES ('Unassigned vendor', -1)
				ON CONFLICT (legacy_id) DO UPDATE SET name = vendors.name RETURNING id`).Scan(&vid); err != nil {
				return err
			}
		}
		bindMode := map[string]string{"transceiver": "trx", "transmitter": "tx", "receiver": "rx"}[strings.ToLower(mode)]
		if bindMode == "" {
			bindMode = "trx"
		}
		tps = max(tps, 1)
		if window <= 0 {
			window = min(max(tps, 10), 100)
		}
		var newID int64
		if err := im.pg.QueryRow(ctx, `INSERT INTO connections (vendor_id, name, host, port, system_id, password_enc, system_type,
				bind_mode, binds, tps, window_size, source_ton, source_npi, dest_ton, dest_npi, status, legacy_id)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17)
			ON CONFLICT (legacy_id) DO UPDATE SET name = EXCLUDED.name, host = EXCLUDED.host, port = EXCLUDED.port,
				system_id = EXCLUDED.system_id, password_enc = EXCLUDED.password_enc, system_type = EXCLUDED.system_type,
				bind_mode = EXCLUDED.bind_mode, binds = EXCLUDED.binds, tps = EXCLUDED.tps, window_size = EXCLUDED.window_size
			RETURNING id`, vid, name, host, port, user, im.cipher.Encrypt(pass), sysType, bindMode, min(max(binds, 1), 32), tps,
			min(window, 1000), sTon, sNpi, dTon, dNpi,
			// Imported connections start disabled so the new gateway does not bind alongside the old one by accident.
			"disabled", id).Scan(&newID); err != nil {
			return fmt.Errorf("connection %q: %w", name, err)
		}
		im.connections[id] = newID
		n++
	}
	im.note("vendors:            %d", len(vendors))
	im.note("connections:        %d SMPP (imported DISABLED: enable each one at cutover), %d HTTP gateways skipped", n, skipped)
	return rows.Err()
}

func (im *importer) networkFor(mcc, mnc string) any {
	mcc, mnc = strings.TrimSpace(mcc), strings.TrimSpace(mnc)
	if mcc == "" || mcc == "0" || mnc == "" || mnc == "0" {
		return nil
	}
	if id := im.networks[mcc+"-"+mnc]; id != 0 {
		return id
	}
	return nil
}

func (im *importer) importClientRates(ctx context.Context) error {
	rows, err := im.my.QueryContext(ctx, `SELECT id, COALESCE(client_id, 0), iso, CAST(mcc AS CHAR), mnc, selling_price, date_created
		FROM client_rates WHERE status = 1`)
	if err != nil {
		return err
	}
	defer rows.Close()
	n, skipped := 0, 0
	for rows.Next() {
		var id, clientID int64
		var iso, mcc, mnc string
		var price float64
		var created time.Time
		if err := rows.Scan(&id, &clientID, &iso, &mcc, &mnc, &price, &created); err != nil {
			return err
		}
		cid := im.clients[clientID]
		country := im.iso(iso)
		if cid == 0 || country == nil {
			skipped++
			continue
		}
		if _, err := im.pg.Exec(ctx, `INSERT INTO client_rates (client_id, country_iso, network_id, price, effective_from, legacy_id)
			VALUES ($1, $2, $3, $4::numeric, $5, $6)
			ON CONFLICT (legacy_id) DO UPDATE SET price = EXCLUDED.price`, cid, country, im.networkFor(mcc, mnc), money(price), created, id); err != nil {
			skipped++
			im.log.Warn("client rate skipped", "legacy_id", id, "err", err)
			continue
		}
		n++
	}
	im.note("client rates:       %d (%d skipped: unknown client or country)", n, skipped)
	return rows.Err()
}

func (im *importer) importVendorRates(ctx context.Context) error {
	rows, err := im.my.QueryContext(ctx, `SELECT id, gateway_id, iso, CAST(mcc AS CHAR), mnc, price, date_created FROM vendor_rates WHERE status = 1`)
	if err != nil {
		return err
	}
	defer rows.Close()
	n, skipped := 0, 0
	for rows.Next() {
		var id, gatewayID int64
		var iso, mcc, mnc string
		var price float64
		var created time.Time
		if err := rows.Scan(&id, &gatewayID, &iso, &mcc, &mnc, &price, &created); err != nil {
			return err
		}
		conn := im.connections[gatewayID]
		country := im.iso(iso)
		if conn == 0 || country == nil {
			skipped++
			continue
		}
		if _, err := im.pg.Exec(ctx, `INSERT INTO vendor_rates (connection_id, country_iso, network_id, price, effective_from, legacy_id)
			VALUES ($1, $2, $3, $4::numeric, $5, $6)
			ON CONFLICT (legacy_id) DO UPDATE SET price = EXCLUDED.price`, conn, country, im.networkFor(mcc, mnc), money(price), created, id); err != nil {
			skipped++
			im.log.Warn("vendor rate skipped", "legacy_id", id, "err", err)
			continue
		}
		n++
	}
	im.note("vendor rates:       %d (%d skipped: unknown connection or country)", n, skipped)
	return rows.Err()
}

func (im *importer) importRoutes(ctx context.Context) error {
	weights := map[int64]map[int64]int{}
	if rows, err := im.my.QueryContext(ctx, `SELECT route_id, gateway_id, percentage FROM routing_distributed_data WHERE status = 1`); err == nil {
		for rows.Next() {
			var route, gw int64
			var pct float64
			if rows.Scan(&route, &gw, &pct) == nil {
				if weights[route] == nil {
					weights[route] = map[int64]int{}
				}
				weights[route][gw] = int(math.Round(pct))
			}
		}
		rows.Close()
	}
	rows, err := im.my.QueryContext(ctx, `SELECT id, route_name, routing_policy_type, COALESCE(country_id, ''), COALESCE(client_id, 0),
			COALESCE(user_id, 0), COALESCE(gateway_data, ''), COALESCE(gateway_id, 0), CAST(COALESCE(mcc, 0) AS CHAR), COALESCE(mnc, ''),
			COALESCE(source, ''), COALESCE(source_rule, 0), status
		FROM routes`)
	if err != nil {
		return err
	}
	defer rows.Close()
	n := 0
	for rows.Next() {
		var id, policy, clientID, userID, gatewayID, rule, status int64
		var name, country, gwData, mcc, mnc, source string
		if err := rows.Scan(&id, &name, &policy, &country, &clientID, &userID, &gwData, &gatewayID, &mcc, &mnc, &source, &rule, &status); err != nil {
			return err
		}
		var gws []int64
		if gatewayID > 0 {
			gws = append(gws, gatewayID)
		}
		for _, g := range strings.Split(gwData, ",") {
			if v, err := strconv.ParseInt(strings.TrimSpace(g), 10, 64); err == nil && v > 0 && v != gatewayID {
				gws = append(gws, v)
			}
		}
		newPolicy := map[int64]string{1: "priority", 2: "priority", 3: "weighted", 4: "lcr"}[policy]
		if newPolicy == "" {
			newPolicy = "priority"
		}
		match, pattern := "any", ""
		if source != "" {
			switch rule {
			case 1:
				match, pattern = "exact", source
			case 2:
				match, pattern = "prefix", source
			case 3:
				match, pattern = "regex", regexp.QuoteMeta(source)+"$"
			case 4:
				match, pattern = "regex", regexp.QuoteMeta(source)
			case 5:
				im.note("WARNING: route %q uses 'sender does not contain %s', which is not supported; imported without the sender rule", name, source)
			}
		}
		st := "disabled"
		if status == 1 {
			st = "active"
		}
		var routeID int64
		if err := im.pg.QueryRow(ctx, `INSERT INTO routes (name, client_id, account_id, country_iso, network_id, sender_match,
				sender_pattern, policy, status, legacy_id)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
			ON CONFLICT (legacy_id) DO UPDATE SET name = EXCLUDED.name, client_id = EXCLUDED.client_id, account_id = EXCLUDED.account_id,
				country_iso = EXCLUDED.country_iso, network_id = EXCLUDED.network_id, sender_match = EXCLUDED.sender_match,
				sender_pattern = EXCLUDED.sender_pattern, policy = EXCLUDED.policy, status = EXCLUDED.status
			RETURNING id`, name, nullable(im.clients[clientID]), nullable(im.smppAccts[userID]), im.iso(country),
			im.networkFor(mcc, mnc), match, pattern, newPolicy, st, id).Scan(&routeID); err != nil {
			return fmt.Errorf("route %q: %w", name, err)
		}
		if _, err := im.pg.Exec(ctx, `DELETE FROM route_targets WHERE route_id = $1`, routeID); err != nil {
			return err
		}
		pos := 0
		for _, gw := range gws {
			conn := im.connections[gw]
			if conn == 0 {
				continue
			}
			w := 100
			if pct, ok := weights[id][gw]; ok {
				w = pct
			}
			if _, err := im.pg.Exec(ctx, `INSERT INTO route_targets (route_id, connection_id, position, weight) VALUES ($1, $2, $3, $4)
				ON CONFLICT DO NOTHING`, routeID, conn, pos, w); err != nil {
				return err
			}
			pos++
		}
		n++
	}
	im.note("routes:             %d", n)
	return rows.Err()
}

// ---- history ---------------------------------------------------------------------------------------------

var statusMap = map[int64][2]string{ // legacy status -> (status, dlr stat)
	0: {"unknown", ""}, 1: {"delivered", "DELIVRD"}, 2: {"undelivered", "UNDELIV"}, 3: {"sent", ""}, 4: {"sent", ""},
	5: {"failed", "REJECTD"}, 6: {"unknown", ""}, 8: {"sent", "ACCEPTD"}, 16: {"rejected", "REJECTD"},
}

// importMessages copies outgoing_sms, its archive and the monthly archive tables. Message ids are derived
// from the legacy table and id, so running it again skips rows already imported.
func (im *importer) importMessages(ctx context.Context) error {
	tables := []string{"outgoing_sms", "outgoing_sms_archive"}
	rows, err := im.my.QueryContext(ctx, `SELECT table_name FROM information_schema.tables WHERE table_schema = DATABASE()
		AND table_name LIKE 'outgoing\_sms\_archive\_%' AND table_name NOT LIKE '%failed%' ORDER BY table_name`)
	if err == nil {
		for rows.Next() {
			var t string
			if rows.Scan(&t) == nil {
				tables = append(tables, t)
			}
		}
		rows.Close()
	}
	// Create monthly partitions for the whole history first, so rows do not land in the default partition.
	var minDate, maxDate sql.NullTime
	for _, t := range tables {
		if !im.tableExists(ctx, t) {
			continue
		}
		var lo, hi sql.NullTime
		_ = im.my.QueryRowContext(ctx, fmt.Sprintf("SELECT MIN(date_created), MAX(date_created) FROM `%s`", t)).Scan(&lo, &hi)
		if lo.Valid && (!minDate.Valid || lo.Time.Before(minDate.Time)) {
			minDate = lo
		}
		if hi.Valid && (!maxDate.Valid || hi.Time.After(maxDate.Time)) {
			maxDate = hi
		}
	}
	if minDate.Valid {
		for m := time.Date(minDate.Time.Year(), minDate.Time.Month(), 1, 0, 0, 0, 0, time.UTC); !m.After(maxDate.Time); m = m.AddDate(0, 1, 0) {
			if _, err := im.pg.Exec(ctx, `SELECT ensure_messages_partition($1)`, m); err != nil {
				return err
			}
		}
	}
	total := 0
	for _, t := range tables {
		if !im.tableExists(ctx, t) {
			continue
		}
		n, err := im.importMessageTable(ctx, t)
		if err != nil {
			return fmt.Errorf("%s: %w", t, err)
		}
		im.note("messages:           %d new rows from %s", n, t)
		total += n
	}
	im.note("messages total:     %d new (already imported rows are skipped)", total)
	return nil
}

func (im *importer) importMessageTable(ctx context.Context, table string) (int, error) {
	const batchSize = 5000
	var lastID int64
	count := 0
	for {
		rows, err := im.my.QueryContext(ctx, fmt.Sprintf("SELECT id, COALESCE(client_id, 0), COALESCE(user_id, 0), COALESCE(protocol, ''), "+
			"COALESCE(sender, ''), destination, COALESCE(message, ''), COALESCE(coding, 0), COALESCE(pages, 1), COALESCE(iso, ''), "+
			"COALESCE(CAST(mcc AS CHAR), ''), COALESCE(mnc, ''), COALESCE(gateway_id, '0'), status, COALESCE(credits_used, 0), "+
			"COALESCE(vendor_rate, 0), COALESCE(external_message_id, ''), date_created, date_sent, delivery_time "+
			"FROM `%s` WHERE id > ? ORDER BY id LIMIT %d", table, batchSize), lastID)
		if err != nil {
			// Older archive tables may lack the newer columns; fall back to the common subset.
			return count, err
		}
		var (
			ids                                        []uuid.UUID
			created                                    []time.Time
			clientIDs, accountIDs, networkIDs, connIDs []*int64
			legacyIDs                                  []int64
			sources, dests, bodies, statuses, dlrStats []string
			countries                                  []*string
			codings, parts                             []int16
			prices, costs, refs                        []string
			sentAt, dlrAt                              []*time.Time
		)
		n := 0
		for rows.Next() {
			var id, clientID, userID, coding, pages, status int64
			var protocol, sender, dest, body, iso, mcc, mnc, gw, ref string
			var credits, vendorRate float64
			var createdAt time.Time
			var dateSent, deliveryTime sql.NullTime
			if err := rows.Scan(&id, &clientID, &userID, &protocol, &sender, &dest, &body, &coding, &pages, &iso, &mcc, &mnc, &gw,
				&status, &credits, &vendorRate, &ref, &createdAt, &dateSent, &deliveryTime); err != nil {
				rows.Close()
				return count, err
			}
			lastID = id
			n++
			cid := im.historyClient(ctx, clientID)
			if cid == 0 {
				continue
			}
			ids = append(ids, uuid.NewSHA1(messageNamespace, []byte(table+":"+strconv.FormatInt(id, 10))))
			created = append(created, createdAt.UTC())
			c := cid
			clientIDs = append(clientIDs, &c)
			var acc *int64
			if protocol == "smpp" {
				if a := im.smppAccts[userID]; a != 0 {
					acc = &a
				}
			}
			accountIDs = append(accountIDs, acc)
			var netID *int64
			if v, ok := im.networkFor(mcc, mnc).(int64); ok {
				netID = &v
			}
			networkIDs = append(networkIDs, netID)
			var conn *int64
			if g, err := strconv.ParseInt(strings.TrimSpace(gw), 10, 64); err == nil {
				if v := im.connections[g]; v != 0 {
					conn = &v
				}
			}
			connIDs = append(connIDs, conn)
			legacyIDs = append(legacyIDs, id)
			sources = append(sources, sender)
			dests = append(dests, dest)
			bodies = append(bodies, body)
			st := statusMap[status]
			if st[0] == "" {
				st = [2]string{"unknown", ""}
			}
			statuses = append(statuses, st[0])
			dlrStats = append(dlrStats, st[1])
			var country *string
			if v, ok := im.iso(iso).(string); ok {
				country = &v
			}
			countries = append(countries, country)
			codings = append(codings, int16(coding))
			parts = append(parts, int16(max(pages, 1)))
			prices = append(prices, money(credits))
			costs = append(costs, money(vendorRate*float64(max(pages, 1))))
			refs = append(refs, ref)
			sentAt = append(sentAt, nullTime(dateSent))
			dlrAt = append(dlrAt, nullTime(deliveryTime))
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return count, err
		}
		if n == 0 {
			return count, nil
		}
		if len(ids) > 0 {
			tag, err := im.pg.Exec(ctx, `INSERT INTO messages (id, created_at, client_id, account_id, source, destination, body,
					data_coding, parts, country_iso, network_id, connection_id, status, dlr_status, price, cost, client_ref,
					sent_at, dlr_at, dlr_sent_at, wants_dlr, legacy_id)
				SELECT u.id, u.created_at, u.client_id, u.account_id, u.source, u.destination, u.body, u.coding, u.parts, u.country,
					u.network_id, u.connection_id, u.status, u.dlr_status, u.price::numeric, u.cost::numeric, u.ref, u.sent_at, u.dlr_at,
					u.dlr_at, false, u.legacy_id
				FROM unnest($1::uuid[], $2::timestamptz[], $3::bigint[], $4::bigint[], $5::text[], $6::text[], $7::text[],
					$8::smallint[], $9::smallint[], $10::text[], $11::bigint[], $12::bigint[], $13::text[], $14::text[], $15::text[],
					$16::text[], $17::text[], $18::timestamptz[], $19::timestamptz[], $20::bigint[])
					AS u(id, created_at, client_id, account_id, source, destination, body, coding, parts, country, network_id,
						connection_id, status, dlr_status, price, cost, ref, sent_at, dlr_at, legacy_id)
				ON CONFLICT (id, created_at) DO NOTHING`,
				ids, created, clientIDs, accountIDs, sources, dests, bodies, codings, parts, countries, networkIDs, connIDs,
				statuses, dlrStats, prices, costs, refs, sentAt, dlrAt, legacyIDs)
			if err != nil {
				return count, err
			}
			count += int(tag.RowsAffected())
		}
		if n < batchSize {
			return count, nil
		}
	}
}

func nullTime(t sql.NullTime) *time.Time {
	if !t.Valid || t.Time.Year() < 2000 {
		return nil
	}
	v := t.Time.UTC()
	return &v
}

// importStats loads the legacy daily report table (mt_reports) into stats_hourly for the days before the new
// gateway started sending, replacing any earlier import of the same days.
func (im *importer) importStats(ctx context.Context) error {
	if !im.tableExists(ctx, "mt_reports") {
		return nil
	}
	cutoff := time.Now().UTC()
	var first *time.Time
	_ = im.pg.QueryRow(ctx, `SELECT min(created_at) FROM messages WHERE legacy_id IS NULL`).Scan(&first)
	if first != nil {
		cutoff = first.UTC()
	}
	cutoffDay := cutoff.Truncate(24 * time.Hour)
	if _, err := im.pg.Exec(ctx, `DELETE FROM stats_hourly WHERE hour < $1`, cutoffDay); err != nil {
		return err
	}
	rows, err := im.my.QueryContext(ctx, `SELECT date_sent, client_id, gateway_id, COALESCE(iso, ''), COALESCE(mcc_mnc, 0), status,
			SUM(total_sent), SUM(COALESCE(total_pages, total_sent)), SUM(COALESCE(credits_used, 0)),
			SUM(COALESCE(vendor_rate, 0) * COALESCE(total_pages, total_sent))
		FROM mt_reports WHERE date_sent < ? GROUP BY 1, 2, 3, 4, 5, 6`, cutoffDay)
	if err != nil {
		return err
	}
	defer rows.Close()
	batch := &pgx.Batch{}
	n := 0
	for rows.Next() {
		var day time.Time
		var clientID, gatewayID, mccmnc, status, sent, pages int64
		var iso string
		var revenue, cost float64
		if err := rows.Scan(&day, &clientID, &gatewayID, &iso, &mccmnc, &status, &sent, &pages, &revenue, &cost); err != nil {
			return err
		}
		cid := im.historyClient(ctx, clientID)
		if cid == 0 {
			continue
		}
		var netID int64
		if mccmnc > 0 {
			s := strconv.FormatInt(mccmnc, 10)
			if len(s) > 3 {
				if v, ok := im.networkFor(s[:3], s[3:]).(int64); ok {
					netID = v
				}
			}
		}
		delivered, undelivered := int64(0), int64(0)
		switch status {
		case 1:
			delivered = sent
		case 2, 16:
			undelivered = sent
		}
		country := ""
		if v, ok := im.iso(iso).(string); ok {
			country = v
		}
		batch.Queue(`INSERT INTO stats_hourly AS s (hour, client_id, connection_id, country_iso, network_id, submitted, sent,
				delivered, undelivered, parts, revenue, cost)
			VALUES ($1, $2, $3, $4, $5, $6, $6, $7, $8, $9, $10::numeric, $11::numeric)
			ON CONFLICT (hour, client_id, connection_id, country_iso, network_id) DO UPDATE SET
				submitted = s.submitted + EXCLUDED.submitted, sent = s.sent + EXCLUDED.sent,
				delivered = s.delivered + EXCLUDED.delivered, undelivered = s.undelivered + EXCLUDED.undelivered,
				parts = s.parts + EXCLUDED.parts, revenue = s.revenue + EXCLUDED.revenue, cost = s.cost + EXCLUDED.cost`,
			day.UTC(), cid, im.connections[gatewayID], country, netID, sent, delivered, undelivered, pages, money(revenue), money(cost))
		n++
	}
	if err := rows.Err(); err != nil {
		return err
	}
	if err := im.pg.SendBatch(ctx, batch).Close(); err != nil {
		return err
	}
	im.note("daily statistics:   %d legacy report rows (days before %s)", n, cutoffDay.Format("2006-01-02"))
	if im.placeholders > 0 {
		im.note("placeholder clients: %d deleted legacy clients recreated as disabled, to keep their history in reports", im.placeholders)
	}
	return nil
}
