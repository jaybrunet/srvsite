/*

SRVsite -- a tiny, database-free website engine.

Version 0.8 by Jay Brunet jay@pjbrunet.com
SRVsite comes with ABSOLUTELY NO WARRANTY; See GPLv2
https://www.gnu.org/licenses/old-licenses/gpl-2.0.html

PRINCIPLES

	Prefer no database. Instead we use toml & json files.
	Avoid excessive error checking; nothing here is mission critical.
	Avoid routes, just use r.URL.Path.
	Minimize external dependencies.

HOW IT WORKS

	The page chrome lives in head.srv, top.srv, nav.srv and foot.srv.
	The page list, navigation tabs, port, site title and contact address
	live in srvsite.toml. Each [[pages]] block names a body file
	(pick_file). A page may also name an optional page-specific config
	file (config) whose SHAPE tells the engine what KIND of page it is --
	a [[posts]] array makes it a blog (see PAGE-SPECIFIC CONFIG below).

	Identity is carried by a server-side session: a random token in an
	HttpOnly/Secure/SameSite cookie. Sessions persist to sessions.json so
	a restart does not log everyone out. License keys are checked against
	users.json, which is the source of truth (edit it by hand).

	For technical support or managed hosting, visit SRVpress.com

*/

package main

import (
	"bufio"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"html/template"
	"log"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/BurntSushi/toml"
)

//===== WEBSITE

type navOption struct {
	URL      string
	Name     string // navigation menu name
	Active   bool   // highlighting the navigation
	PageHead string // .page-head
	PickFile string // the content for the page
	Config   string // optional page-specific config file (see PAGE-SPECIFIC CONFIG)
}
type navOptionsPage struct {
	SiteTitle  string // main title tag
	BasePath   string // URL prefix when served under a subdirectory
	Title      string // Active title highlighted in nav
	Options    []navOption
	LoggedIn   bool   // true when a valid session cookie is present
	LicenseKey string // the caller's own license key (for a members page)
	// PageBody is the rendered body for the current page. It is passed as
	// DATA (not template source), so page content files (blog posts, etc.)
	// may contain ANY pasted HTML/CSS/JS -- including "{{" -- without being
	// reinterpreted as a template directive.
	PageBody template.HTML
}

func getNavOptions(cfg SiteConfig) navOptionsPage {
	// Pages come straight from srvsite.toml. To add a page (and its nav tab),
	// just add another [[pages]] block -- no code change needed here.
	var newOptions []navOption
	for _, p := range cfg.Pages {
		newOptions = append(newOptions, navOption{
			URL:      cfg.BasePath + p.URL,
			Name:     p.Name,
			Active:   false,
			PageHead: p.PageHead,
			PickFile: p.PickFile,
			Config:   p.Config, // optional page-specific config file
		})
	}

	return navOptionsPage{
		SiteTitle: cfg.SiteTitle,
		BasePath:  cfg.BasePath,
		Title:     "Undefined", // set by drawPage using r.URL.Path
		Options:   newOptions,
	}
} // getNavOptions

// Basic hardening headers. Applied to every HTML page.
func setSecurityHeaders(w http.ResponseWriter) {
	h := w.Header()
	h.Set("X-Frame-Options", "DENY")
	h.Set("X-Content-Type-Options", "nosniff")
	h.Set("Referrer-Policy", "no-referrer")
	h.Set("Content-Security-Policy", "default-src 'self'; img-src 'self' data:; style-src 'self' 'unsafe-inline' https://fonts.googleapis.com; font-src 'self' https://fonts.gstatic.com; script-src 'self' 'unsafe-inline'; frame-ancestors 'none'")
}

func drawPage(w http.ResponseWriter, r *http.Request, cfg SiteConfig) {

	// This function builds HTML pages in realtime by concatenating these strings.
	// Expected: head.srv, top.srv, nav.srv, foot.srv + any files listed in srvsite.toml

	setSecurityHeaders(w)

	// When served under a subdirectory (base_path in srvsite.toml), strip that
	// prefix so page matching and generated links stay correct behind a proxy
	// that forwards the full path.
	localPath := r.URL.Path
	if cfg.BasePath != "" {
		localPath = strings.TrimPrefix(localPath, cfg.BasePath)
		if localPath == "" {
			localPath = "/"
		}
	}

	head := fileToStr("head.srv")
	top := fileToStr("top.srv")
	nav := fileToStr("nav.srv")
	middleTop := `<div id="middle">`
	// "pick" content goes here, according to srvsite.toml
	middleBottom := `</div><!-- #middle -->`
	foot := fileToStr("foot.srv")
	footjs := fileToStr("foot.js")
	// end HTML structure

	data := getNavOptions(cfg) // cfg is loaded once at startup

	// Identity comes from the session cookie, never from the URL.
	sess, loggedIn := getSession(r)
	data.LoggedIn = loggedIn
	if loggedIn {
		_, data.LicenseKey = GetLicenseKeyBySaleID(sess.SaleID)
		// Refresh the browser cookie on every authenticated pageview so
		// it slides forward and effectively never expires for an active
		// user (the server-side TTL is the real limit).
		if c, err := r.Cookie(sessionCookie); err == nil {
			setSessionCookie(w, r, c.Value)
		}
	}

	var pick string
	is404 := true
	// The template has .Active conditional that highlights nav tab for the page we're on.
	for i := range data.Options {
		if localPath == strings.TrimPrefix(data.Options[i].URL, cfg.BasePath) {
			data.Title = data.Options[i].Name
			data.Options[i].Active = true
			is404 = false
			// The page body may be a plain .srv file, or generated from an
			// optional page-specific config file (a blog, a FAQ, ...).
			pick = renderPageBody(data.Options[i])

			// Pages under a login (e.g. the members /studio page) are only
			// served to a valid session. A guest gets a plain 404, so the
			// page's existence is not advertised.
			if localPath == "/studio" && !loggedIn {
				is404 = true
			}
		}
	}

	if is404 { // post not found
		// A real 404 (no redirect) so random scanners get nothing useful
		// and don't get bounced onto the homepage.
		fmt.Println("\033[0;31m404\033[0m")
		w.WriteHeader(http.StatusNotFound)
		fmt.Fprint(w, "<!DOCTYPE html><html><head><title>404 Not Found</title></head><body><h1>404 Not Found</h1></body></html>")
		return
	}

	// gowebexamples.com/templates/ levelup.gitconnected.com/learn-and-use-templates-in-go-aa6146b01a38
	// The page body is injected as DATA ({{.PageBody}}), never spliced into the
	// template source, so pasted HTML/CSS/JS is treated as literal content.
	data.PageBody = template.HTML(pick)

	t, _ := template.New("moo").Parse(head + top + nav + middleTop + `{{.PageBody}}` + middleBottom + foot + footjs)
	t.Execute(w, data)

	fmt.Fprintf(w, "</body></html>")

	// Log to console. Check on this in a tmux session. Ignores 404s.
	strSum := sumASCII(r.Header.Get("User-Agent") + r.Header.Get("x-real-ip"))
	fmt.Println(strSum + " " + time.Now().Format("3:04pm 1-2-2006") + " " + r.URL.Path + " " + r.Header.Get("Referer"))
} // drawPage

//===== MISC

func fileToStr(file string) string { // Why isn't this built into the language?
	tempBytes, _ := os.ReadFile(file)
	return string(tempBytes)
}

func sumASCII(s string) string {
	var sum int
	for _, char := range s {
		sum += int(char)
	}
	return fmt.Sprintf("%d", sum)
}

// Mask a license key for logs, e.g. DEMO0001-****...0001
func maskKey(k string) string {
	if len(k) <= 12 {
		return "****"
	}
	return k[:8] + "-****-" + k[len(k)-4:]
}

// Write a file atomically (temp file + rename) so a crash or a partial write
// can never leave users.json / leads.json truncated or invalid.
func atomicWriteFile(name string, data []byte) error {
	tmp := name + ".tmp"
	if err := os.WriteFile(tmp, data, 0644); err != nil {
		return err
	}
	return os.Rename(tmp, name)
}

//===== INCOMING FORMS & AJAX

// handleForm receives a single-key JSON object from the browser, e.g.
//
//	{"srvsite_contact":"{\"name\":\"...\",\"email\":\"...\",\"message\":\"...\"}"}
//	{"srvsite_login":"DEMO0001-...."}
//
// The part after the last underscore is the "category". Add a new case below
// to handle a new kind of form.
func handleForm(w http.ResponseWriter, r *http.Request) {

	type MyData map[string]string

	decoder := json.NewDecoder(r.Body)
	var t MyData
	err := decoder.Decode(&t)
	if err != nil {
		fmt.Fprintf(w, "Decode() err: %v", err)
		return
	}

	// Process incoming AJAX values
	for k, v := range t {

		// Checksum of user-agent, helpful to follow logs.
		strSum := sumASCII(r.Header.Get("User-Agent") + r.Header.Get("x-real-ip"))

		v = sanitizeLog(v)
		update := strSum + " " + time.Now().Format("15:04:05 1-2-06") + " " + v

		// get string after the underscore
		formCat := k[strings.LastIndex(k, "_")+1:]

		switch formCat {
		case "contact":
			// A public contact-form submission: append it to leads.json and
			// (if configured) email the address in srvsite.toml.
			saveLead(v, r)
			logCat(update, formCat)

		case "login":
			found, saleID := verifyLicenseKey(v)
			if found {
				createSession(w, r, saleID)
				logCat(strSum+" "+time.Now().Format("15:04:05 1-2-06")+" "+maskKey(v)+" valid", formCat)
				w.Write([]byte("ok"))
			} else {
				logCat(strSum+" "+time.Now().Format("15:04:05 1-2-06")+" "+maskKey(v)+" invalid", formCat)
				w.Write([]byte("fail"))
			}
			return

		default:
			// invalid AJAX
			fmt.Println("\033[0;33m" + formCat + "\033[0m " + update)
		}
	} // should only have one key/value pair

} // handleForm

//===== SESSIONS

// Identity is proven once (by the license key) and then carried by an opaque
// server-side session token stored in an HttpOnly cookie. Nothing in the URL,
// and nothing the client can fabricate, grants access.
//
// Sessions are PERSISTED to sessionFile so they survive a restart. The file
// holds the raw session tokens, so it is written 0600 and should be treated
// like a password store. No database, just a JSON file.

const sessionCookie = "srv_session"

// How long an idle session lives. Kept long so a client is not logged out
// mid-project; every authenticated pageview refreshes it (sliding window).
const sessionTTL = 30 * 24 * time.Hour

// The browser cookie is asked to live as long as browsers allow. Chrome caps
// cookie Max-Age at 400 days and silently clamps anything larger, so 400 days
// is the practical maximum. The server keeps its own (shorter) TTL above; the
// cookie is only the envelope, and the token inside it is what really decides
// access. It is re-issued on every authenticated pageview (see drawPage), so
// an active user's cookie never reaches this ceiling.
const cookieMaxAge = 400 * 24 * time.Hour

const sessionFile = "sessions.json"

type Session struct {
	SaleID  string
	Expires time.Time
}

var (
	sessions   = make(map[string]*Session)
	sessionsMu sync.RWMutex

	// Set whenever the session map changes. Sliding-expiration touches are
	// batched: they only set this flag, and flushSessions persists them a few
	// times a minute, so a busy site does not rewrite the file per request.
	sessionsDirty bool
)

func secureCookie(r *http.Request) bool {
	return r.TLS != nil || r.Header.Get("X-Forwarded-Proto") == "https"
}

// setSessionCookie writes the session cookie. Its Max-Age is deliberately the
// largest value browsers honour (see cookieMaxAge) so the browser keeps it for
// as long as possible.
func setSessionCookie(w http.ResponseWriter, r *http.Request, token string) {
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookie,
		Value:    token,
		Path:     "/",
		HttpOnly: true,
		Secure:   secureCookie(r),
		SameSite: http.SameSiteStrictMode,
		MaxAge:   int(cookieMaxAge.Seconds()),
	})
}

func createSession(w http.ResponseWriter, r *http.Request, saleID string) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		fmt.Println("session: rand error", err)
		return
	}
	token := base64.RawURLEncoding.EncodeToString(b)

	sessionsMu.Lock()
	sessions[token] = &Session{SaleID: saleID, Expires: time.Now().Add(sessionTTL)}
	sessionsDirty = true
	sessionsMu.Unlock()

	// Persist immediately so a restart right after login keeps the user in.
	saveSessions()

	setSessionCookie(w, r, token)
}

func getSession(r *http.Request) (*Session, bool) {
	c, err := r.Cookie(sessionCookie)
	if err != nil || c.Value == "" {
		return nil, false
	}
	sessionsMu.RLock()
	sess, ok := sessions[c.Value]
	sessionsMu.RUnlock()
	if !ok {
		return nil, false
	}
	if time.Now().After(sess.Expires) {
		destroySession(c.Value)
		return nil, false
	}
	// Sliding expiration: keep an active session alive. The flush of this
	// update is batched (see sessionsDirty / flushSessions), so we only mark
	// the map dirty here rather than writing the file on every request.
	sessionsMu.Lock()
	sess.Expires = time.Now().Add(sessionTTL)
	sessionsDirty = true
	sessionsMu.Unlock()
	return sess, true
}

func destroySession(token string) {
	sessionsMu.Lock()
	delete(sessions, token)
	sessionsDirty = true
	sessionsMu.Unlock()
}

func logout(w http.ResponseWriter, r *http.Request) {
	if c, err := r.Cookie(sessionCookie); err == nil {
		destroySession(c.Value)
	}
	// Persist the removal right away so a restart cannot resurrect a token
	// the user just signed out of.
	saveSessions()

	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookie,
		Value:    "",
		Path:     "/",
		HttpOnly: true,
		Secure:   secureCookie(r),
		SameSite: http.SameSiteStrictMode,
		MaxAge:   -1,
	})
	http.Redirect(w, r, siteCfg.BasePath+"/", http.StatusSeeOther)
}

//===== SESSION PERSISTENCE

// saveSessions writes every live session to sessionFile. Expired entries are
// dropped first so the file does not grow forever. Callers must not hold
// sessionsMu.
func saveSessions() {
	now := time.Now()

	sessionsMu.Lock()
	live := make(map[string]*Session, len(sessions))
	for token, sess := range sessions {
		if now.After(sess.Expires) {
			continue // expired: let it go
		}
		live[token] = &Session{SaleID: sess.SaleID, Expires: sess.Expires}
	}
	sessionsDirty = false
	sessionsMu.Unlock()

	// The in-memory map is the source of truth, but a failed write during
	// startup must never clobber a valid file with an empty snapshot.
	if len(live) == 0 {
		if _, err := os.Stat(sessionFile); err != nil {
			return // nothing on disk either; no need to create an empty file
		}
	}

	data, err := json.MarshalIndent(live, "", "  ")
	if err != nil {
		fmt.Println("session: marshal error", err)
		return
	}

	// Atomic write (temp + rename) so a crash can never truncate the file.
	// 0600: the tokens in here are as good as the login itself.
	tmp := sessionFile + ".tmp"
	if err := os.WriteFile(tmp, data, 0600); err != nil {
		fmt.Println("session: write error", err)
		return
	}
	if err := os.Rename(tmp, sessionFile); err != nil {
		fmt.Println("session: rename error", err)
	}
}

// loadSessions restores sessions from sessionFile at startup. Expired entries
// are skipped. A missing file (first run) is not an error.
func loadSessions() {
	data, err := os.ReadFile(sessionFile)
	if err != nil {
		if !os.IsNotExist(err) {
			fmt.Println("session: read error", err)
		}
		return
	}

	restored := make(map[string]*Session)
	if err := json.Unmarshal(data, &restored); err != nil {
		fmt.Println("session: parse error (starting with no sessions)", err)
		return
	}

	now := time.Now()
	n := 0
	sessionsMu.Lock()
	for token, sess := range restored {
		if sess == nil || token == "" || now.After(sess.Expires) {
			continue // malformed or expired: drop it
		}
		sessions[token] = &Session{SaleID: sess.SaleID, Expires: sess.Expires}
		n++
	}
	sessionsMu.Unlock()

	if n > 0 {
		fmt.Printf("Restored %d session(s) from %s\n", n, sessionFile)
	}

	// If anything was dropped (expired/malformed), rewrite the file now so it
	// is pruned at startup rather than carrying dead tokens around. This also
	// self-heals a file that somehow lost its permissions.
	if n != len(restored) {
		saveSessions()
	}
}

// flushSessions periodically writes pending changes (sliding-expiration
// updates) so they survive a restart even if the user only browsed around.
// It runs for the life of the process.
func flushSessions() {
	for {
		time.Sleep(time.Minute)
		sessionsMu.RLock()
		dirty := sessionsDirty
		sessionsMu.RUnlock()
		if dirty {
			saveSessions()
		}
	}
}

//===== LOGS

// Basic sanitizer of incoming AJAX values for logs.
func sanitizeLog(input string) string {
	// Replace newlines to prevent log injection
	input = strings.ReplaceAll(input, "\n", " ")
	input = strings.ReplaceAll(input, "\r", " ")
	return input
}

// Security warning, the code calling this is responsible for ensuring (category).log is safe.
func logCat(update string, category string) {
	// Open file in append mode, create if doesn't exist
	file, err := os.OpenFile(category+".log", os.O_APPEND|os.O_WRONLY|os.O_CREATE, 0644)
	defer file.Close() // Ensure the file is closed after this function ends
	if err != nil {
		fmt.Println(err)
		return
	}
	writer := bufio.NewWriter(file)
	_, err = writer.WriteString(update + "\n")

	if err != nil {
		fmt.Println(err)
	}
	writer.Flush()
}

//===== USERS & LOGIN

// The site config, kept package-level so lead handling can reach leads_email
// and site_title without threading cfg through every call. Set once in main().
var siteCfg SiteConfig

// This code is used to lookup, check and retrieve license_key and sale_id.
// users.json is the source of truth and is edited by hand (see README.md).
type LicenseRecord struct {
	SaleID     string `json:"sale_id"`
	LicenseKey string `json:"license_key"`
	Comment    string `json:"comment"`
}

func LoadLicenseRecords() ([]LicenseRecord, error) {
	users, err := os.Open("users.json")
	if err != nil {
		return nil, err
	}
	defer users.Close()

	var recs []LicenseRecord
	dec := json.NewDecoder(users)
	if err := dec.Decode(&recs); err != nil {
		return nil, err
	}
	return recs, nil
}

func GetLicenseKeyBySaleID(target string) (bool, string) {
	recs, _ := LoadLicenseRecords()
	for _, rec := range recs {
		if rec.SaleID == target {
			return true, rec.LicenseKey
		}
	}
	return false, ""
}

func GetSaleIDByLicenseKey(target string) (bool, string) {
	recs, _ := LoadLicenseRecords()
	for _, rec := range recs {
		if rec.LicenseKey == target {
			return true, rec.SaleID
		}
	}
	return false, ""
}

func verifyLicenseKey(licenseKey string) (bool, string) { // VERIFY LOGINS
	// License key comes from AJAX via the login form
	if len(licenseKey) == 35 { // Off to a good start
		found, saleID := GetSaleIDByLicenseKey(licenseKey)
		if found {
			return true, saleID
		} else {
			fmt.Println("\033[0;31minvalid\033[0m licenseKey " + maskKey(licenseKey))
		}
	} else {
		fmt.Println("\033[0;31mbad\033[0m licenseKey " + maskKey(licenseKey))
	}
	return false, ""
}

//===== CONTACT FORM (leads)

// A generic contact-form submission. The browser posts one JSON object:
//
//	{"name":"...","email":"...","message":"..."}
//
// which is appended to leads.json and, if leads_email is set in
// srvsite.toml, emailed to that address.
type LeadRecord struct {
	Name    string `json:"name"`
	Email   string `json:"email"`
	Message string `json:"message"`
	When    string `json:"when"`
}

func saveLead(v string, r *http.Request) {
	var newLead LeadRecord
	json.Unmarshal([]byte(v), &newLead)
	newLead.When = time.Now().Format("2006-01-02 15:04:05")

	var leads []LeadRecord
	if content, err := os.ReadFile("leads.json"); err == nil {
		json.Unmarshal(content, &leads)
	}

	leads = append(leads, newLead)
	formatted, _ := json.MarshalIndent(leads, "", "  ")
	atomicWriteFile("leads.json", formatted)

	// Email the address configured in srvsite.toml (leads_email) so the owner
	// is told when someone completes the form. Best-effort only.
	sendLeadEmail(newLead, r)
}

// sendLeadEmail notifies the address configured in srvsite.toml (leads_email)
// that a new contact form submission arrived. Mail is handed to the local
// MTA, so plain "mail -s" is enough. This is best-effort: a delivery problem
// must never break the form, so errors are only logged, never returned.
func sendLeadEmail(lead LeadRecord, r *http.Request) {
	if siteCfg.LeadsEmail == "" {
		return // notifications not configured
	}

	// Client IP: a reverse proxy sets x-real-ip; fall back to RemoteAddr for
	// a direct connection.
	ip := r.Header.Get("x-real-ip")
	if ip == "" {
		ip, _, _ = net.SplitHostPort(r.RemoteAddr)
	}

	var b strings.Builder
	b.WriteString("New contact form submission\n\n")
	fmt.Fprintf(&b, "Name:    %s\n", lead.Name)
	fmt.Fprintf(&b, "Email:   %s\n", lead.Email)
	fmt.Fprintf(&b, "Message: %s\n", lead.Message)
	b.WriteString("\n--- server ---\n")
	fmt.Fprintf(&b, "When: %s\n", time.Now().Format("2006-01-02 15:04:05 MST"))
	fmt.Fprintf(&b, "IP:   %s\n", ip)
	if host, err := os.Hostname(); err == nil {
		fmt.Fprintf(&b, "Host: %s\n", host)
	}
	fmt.Fprintf(&b, "Site: %s\n", siteCfg.SiteTitle)

	subject := "New message on " + siteCfg.SiteTitle

	cmd := exec.Command("mail", "-s", subject, siteCfg.LeadsEmail)
	cmd.Stdin = strings.NewReader(b.String())
	if out, err := cmd.CombinedOutput(); err != nil {
		fmt.Println("\033[0;31mlead mail failed:\033[0m " + err.Error() + " " + strings.TrimSpace(string(out)))
		return
	}
	fmt.Println("lead mail sent to " + siteCfg.LeadsEmail)
}

//===== CONFIG (TOML)

// SiteConfig mirrors srvsite.toml. Add a new [[pages]] block there to add a
// page + navigation tab; the Go side needs no changes.
type PageConfig struct {
	URL      string `toml:"url"`
	Name     string `toml:"name"`
	PageHead string `toml:"page_head"`
	PickFile string `toml:"pick_file"`
	// Config is OPTIONAL. When set, it names a page-specific TOML file whose
	// shape (e.g. a [[posts]] array) tells srvsite.go what KIND of page this
	// is. A page without a Config renders exactly like before.
	Config string `toml:"config"`
}

type SiteConfig struct {
	Port       int          `toml:"port"`
	SiteTitle  string       `toml:"site_title"`
	LeadsEmail string       `toml:"leads_email"` // where contact-form notifications are sent
	BasePath   string       `toml:"base_path"`   // optional URL prefix when served under a subdirectory
	Pages      []PageConfig `toml:"pages"`
}

func loadConfig(filename string) SiteConfig {
	var cfg SiteConfig
	if _, err := toml.DecodeFile(filename, &cfg); err != nil {
		fmt.Println("\033[0;31mconfig error:\033[0m " + err.Error())
	}
	return cfg
}

//===== PAGE-SPECIFIC CONFIG

// A page may name an optional page-specific config file in srvsite.toml (the
// "config" key of a [[pages]] block). The SHAPE of that file tells srvsite.go
// what KIND of page this is, so it can be rendered differently than a plain
// .srv body. Detection is by structure, not filename, so the file can be named
// anything. To add a new kind of page later (e.g. a FAQ with a faq.toml), add
// a Kind constant, teach detectPageKind() its signature key, and add a case to
// renderPageBody().

type PageKind string

const (
	KindPlain PageKind = ""     // no page config, or an unrecognised one
	KindBlog  PageKind = "blog" // a page config containing a [[posts]] array
)

// pageMeta is the decoded-once view of a page-specific TOML file.
type pageMeta struct {
	Kind PageKind
	File string
	Data []byte
	Raw  map[string]interface{}
}

// detectPageKind inspects which known array-of-tables keys a page config
// defines. A [[posts]] array marks a blog; future kinds hang off their own
// signature arrays.
func detectPageKind(raw map[string]interface{}) PageKind {
	if _, ok := raw["posts"].([]map[string]interface{}); ok {
		return KindBlog
	}
	return KindPlain
}

func loadPageMeta(path string) *pageMeta {
	data, err := os.ReadFile(path)
	if err != nil {
		fmt.Println("\033[0;31mpage config error:\033[0m " + path + ": " + err.Error())
		return nil
	}
	var raw map[string]interface{}
	if _, err := toml.Decode(string(data), &raw); err != nil {
		fmt.Println("\033[0;31mpage config error:\033[0m " + path + ": " + err.Error())
		return nil
	}
	if len(raw) == 0 {
		fmt.Println("\033[0;31mpage config error:\033[0m " + path + ": empty")
		return nil
	}
	return &pageMeta{Kind: detectPageKind(raw), File: path, Data: data, Raw: raw}
}

// renderPageBody produces the body for a page. Pages with no config, or with a
// config we don't recognise yet, render their pick_file (.srv) unchanged, so
// adding this feature can never break an existing plain page.
func renderPageBody(opt navOption) string {
	pick := fileToStr(opt.PickFile)
	if opt.Config == "" {
		return pick
	}
	meta := loadPageMeta(opt.Config)
	if meta == nil {
		return pick // config unreadable -> fall back to the plain .srv body
	}
	switch meta.Kind {
	case KindBlog:
		// The .srv shell (here: just the page CSS) plus the generated blog.
		return pick + drawBlogBody(meta)
	default:
		return pick
	}
}

//===== BLOG PAGES

// BlogConfig is the typed shape of a blog page config file (blog.toml). Post
// bodies live in separate files so the TOML stays small; those files may hold
// any HTML/CSS/JS (not markdown).
type BlogPost struct {
	ID      string `toml:"id"`    // slug, used in the URL hash (#id)
	Title   string `toml:"title"` // headline
	Date    string `toml:"date"`  // free text, e.g. "March 4, 2026"
	Author  string `toml:"author"`
	Image   string `toml:"image"`   // optional 1200x800 hero image
	Audio   string `toml:"audio"`   // optional MP3 narration
	Excerpt string `toml:"excerpt"` // 1-2 sentences for the preview card
	File    string `toml:"file"`    // content file, relative to content_dir
}

type BlogConfig struct {
	ContentDir string     `toml:"content_dir"` // dir prepended to each post File
	Intro      string     `toml:"intro"`       // HTML shown above the reader
	AuthorBio  string     `toml:"author_bio"`  // HTML shown under every post
	Posts      []BlogPost `toml:"posts"`
}

// esc3 escapes the same three characters the original blog JS escaped
// (& < >), in the same order, so the rendered page looks identical.
func esc3(s string) string {
	s = strings.ReplaceAll(s, "&", "&amp;")
	s = strings.ReplaceAll(s, "<", "&lt;")
	return strings.ReplaceAll(s, ">", "&gt;")
}

// blogGradient reproduces (bit-for-bit) the client-side gradientFor() the blog
// used when a post had no image, so image-less posts look the same as before.
func blogGradient(title string) string {
	hash := 0
	for _, ch := range title {
		hash = (hash*31 + int(ch)) % 100000
	}
	hue := hash % 360
	hue2 := (hue + 45) % 360
	return fmt.Sprintf("linear-gradient(135deg, hsl(%d,45%%,28%%), hsl(%d,55%%,15%%))", hue, hue2)
}

func blogBg(image, title string) string {
	if image != "" {
		return "background-image:url('" + image + "');"
	}
	return "background-image:" + blogGradient(title) + ";"
}

// drawBlogPost renders one post exactly like the old client-side showPost().
func drawBlogPost(p BlogPost, bio, body string) string {
	audio := ""
	if p.Audio != "" {
		audio = `<audio id="post-audio" src="` + p.Audio + `" preload="none"></audio>` +
			`<button class="audio-btn" id="audio-toggle" type="button" aria-label="Play audio narration">[&#9654;] Blog Audio</button>`
	}
	return `<div id="post-hero" style="` + blogBg(p.Image, p.Title) + `">` +
		`<div class="hero-inner"><h1>` + esc3(p.Title) + `</h1></div>` +
		`</div>` +
		`<div id="post-byline">` +
		`<div class="byline-meta">By ` + esc3(p.Author) + ` &nbsp;&middot;&nbsp; ` + esc3(p.Date) + `</div>` +
		audio +
		`</div>` +
		`<div id="post-content">` + body +
		`<div id="post-author">` + bio + `</div>` +
		`</div>`
}

// drawBlogCard renders one preview card exactly like the old renderPreviews().
func drawBlogCard(i int, p BlogPost) string {
	return `<div class="post-card" data-index="` + strconv.Itoa(i) + `" id="card-` + p.ID + `">` +
		`<div class="thumb" style="` + blogBg(p.Image, p.Title) + `">` +
		`<div class="thumb-title">` + esc3(p.Title) + `</div>` +
		`</div>` +
		`<div class="card-body">` +
		`<div class="card-date">` + esc3(p.Date) + `</div>` +
		`<div class="card-excerpt">` + esc3(p.Excerpt) + `</div>` +
		`<div class="card-action">More &rarr;</div>` +
		`</div>` +
		`</div>`
}

// drawBlogBody generates the whole blog body (container, intro, reader, cards
// and a small runtime script) from the page's config file. The post markup and
// card markup are built server-side; the script only swaps the reader and
// moves the "active" highlight.
func drawBlogBody(meta *pageMeta) string {
	var bc BlogConfig
	if _, err := toml.Decode(string(meta.Data), &bc); err != nil {
		fmt.Println("\033[0;31mblog config error:\033[0m " + meta.File + ": " + err.Error())
		return ""
	}

	type jsPost struct {
		ID   string `json:"id"`
		HTML string `json:"html"`
	}
	frags := make([]jsPost, 0, len(bc.Posts))
	var cards strings.Builder
	for i, p := range bc.Posts {
		body := fileToStr(filepath.Join(bc.ContentDir, p.File))
		frags = append(frags, jsPost{ID: p.ID, HTML: drawBlogPost(p, bc.AuthorBio, body)})
		cards.WriteString("\n\t\t\t" + drawBlogCard(i, p))
	}

	// The first post shows by default (same as the old JS behaviour).
	first := ""
	if len(frags) > 0 {
		first = frags[0].HTML
	}

	// json.Marshal escapes < > & so the payload is safe inside <script>.
	payload, _ := json.Marshal(map[string]interface{}{"posts": frags})

	var b strings.Builder
	b.WriteString(`<div id="content-lower">`)
	b.WriteString("\n\t<div id=\"blog-intro\">" + bc.Intro + "</div>")
	b.WriteString("\n\t<div id=\"post-reader\">" + first + "</div>")
	b.WriteString("\n\t<div id=\"post-grid-section\">\n\t\t<div id=\"post-grid\">" + cards.String() + "\n\t\t</div>\n\t</div>\n")
	b.WriteString("</div>\n")
	b.WriteString(blogRuntimeJS(payload))
	return b.String()
}

// blogRuntimeJS emits the minimal client-side glue: the server already built
// every post fragment, so this only swaps the reader, wires the audio button,
// and keeps the preview-card highlight in sync (plus #hash deep links).
func blogRuntimeJS(payload []byte) string {
	return `<script>
/* The blog design and data are generated server-side in srvsite.go from this
   page's config file (blog.toml). This script only swaps the reader and keeps
   the preview-card highlight in sync with the URL hash. */
var SRV_BLOG = ` + string(payload) + `;
function srvShowPost(i, scroll) {
	var p = SRV_BLOG.posts[i]; if (!p) return;
	var reader = document.getElementById("post-reader"); if (!reader) return;
	reader.innerHTML = p.html;
	var a = document.getElementById("audio-toggle");
	if (a) {
		var el = document.getElementById("post-audio");
		a.onclick = function () { if (!el) return; if (el.paused) el.play(); else el.pause(); };
		el.onplay = function () { a.innerHTML = "[&#10073;&#10073;] Pause Audio"; };
		el.onpause = function () { a.innerHTML = "[&#9654;] Blog Audio"; };
		el.onended = function () { a.innerHTML = "[&#9654;] Blog Audio"; };
	}
	var cards = document.getElementsByClassName("post-card");
	for (var c = 0; c < cards.length; c++) {
		var act = cards[c].querySelector(".card-action");
		if (parseInt(cards[c].getAttribute("data-index"), 10) === i) {
			cards[c].className = "post-card active";
			cards[c].style.display = "none";
			if (act) act.innerHTML = "Currently Reading &bull;";
		} else {
			cards[c].className = "post-card";
			cards[c].style.display = "";
			if (act) act.innerHTML = "More &rarr;";
		}
	}
	if (scroll) {
		reader.scrollIntoView({ behavior: "smooth", block: "start" });
		if (history.pushState) history.pushState(null, null, "#" + p.id);
		else window.location.hash = p.id;
	}
}
function srvInitBlog() {
	var cards = document.getElementsByClassName("post-card");
	for (var c = 0; c < cards.length; c++) {
		cards[c].onclick = function () {
			srvShowPost(parseInt(this.getAttribute("data-index"), 10), true);
		};
	}
	var start = 0, h = window.location.hash.replace("#", "");
	if (h) {
		for (var i = 0; i < SRV_BLOG.posts.length; i++) {
			if (SRV_BLOG.posts[i].id === h) { start = i; break; }
		}
	}
	srvShowPost(start, false);
	window.addEventListener("hashchange", function () {
		var cur = window.location.hash.replace("#", "");
		if (cur) {
			for (var i = 0; i < SRV_BLOG.posts.length; i++) {
				if (SRV_BLOG.posts[i].id === cur) { srvShowPost(i, true); break; }
			}
		}
	});
}
$(function () { srvInitBlog(); });
</script>
`
}

//===== END OF PAGE-SPECIFIC CONFIG

//===== MAIN

func main() {

	fmt.Println("---------------------------------")
	fmt.Println(" Starting SRVsite version 0.8")
	fmt.Println("---------------------------------")

	// Config only loaded once, not per pageview.
	// If you change srvsite.toml, restart the app (or let entr do it).
	cfg := loadConfig("srvsite.toml")
	siteCfg = cfg

	// Restore sessions saved by the previous run so a restart does not log
	// everybody out, then keep them flushed in the background.
	loadSessions()
	go flushSessions()

	// This dir contains images, style.css, javascript, etc.
	http.Handle("/assets/", http.StripPrefix("/assets/", http.FileServer(http.Dir("assets"))))
	// When served under a subdirectory (base_path), also expose assets at the
	// prefixed path so absolute asset URLs resolve through the proxy.
	if cfg.BasePath != "" {
		http.Handle(cfg.BasePath+"/assets/", http.StripPrefix(cfg.BasePath+"/assets/", http.FileServer(http.Dir("assets"))))
	}
	http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		drawPage(w, r, cfg)
	})
	http.HandleFunc("/form", handleForm) // AJAX requests we send from forms.
	http.HandleFunc("/logout", logout)
	// Also serve the AJAX endpoints under the base path, since relative URLs
	// on a page served from a subdirectory resolve to that prefix.
	if cfg.BasePath != "" {
		http.HandleFunc(cfg.BasePath+"/form", handleForm)
		http.HandleFunc(cfg.BasePath+"/logout", logout)
	}

	// Sleep to avoid error "bind: address already in use" -- sleeps once, not per pageview.
	time.Sleep(time.Millisecond * 100)

	// TLS is terminated by the reverse proxy (Caddy), which obtains and renews
	// certificates automatically. This app only ever speaks plain HTTP behind it.
	port := strconv.Itoa(cfg.Port)
	fmt.Println("Listening on port " + port)
	log.Fatal(http.ListenAndServe(":"+port, nil))
}
