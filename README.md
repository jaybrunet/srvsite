# SRVsite

**SRVsite** is a tiny, database-free website engine written in Go. It keeps
its configuration in **TOML**, its users in **users.json** and its content in
plain **.srv** files — no database, no build step, no framework. Adding a page
is one `[[pages]]` block in `srvsite.toml` plus a body file.

* Version 0.8 by Jay Brunet <jay@pjbrunet.com>
* SRVsite comes with ABSOLUTELY NO WARRANTY; See GPLv2
  <https://www.gnu.org/licenses/old-licenses/gpl-2.0.html>

> **For technical support or managed hosting, visit [SRVpress.com](https://srvpress.com).**
> SRVsite is sponsored by SRVpress.com, a small hosting provider that has
> hosted websites (and WordPress) since the 1990s.

---

## Purpose

SRVsite renders every page at request time by concatenating a handful of
small template files:

```
head.srv  +  top.srv  +  nav.srv  +  <page body>  +  foot.srv  +  foot.js
```

The page body comes from a `.srv` file named by the page's `[[pages]]` block.
A page may also name an **optional config file** whose *shape* tells the
engine what kind of page it is — a `[[posts]]` array makes it a **blog**, and
the post cards and reader are then generated from that config. The same
mechanism can be extended to other page kinds (see `feature-ideas.md`).

Everything is deliberately minimal: no database, few dependencies, and page
content files may contain any HTML/CSS/JS (including `{{`) because the body is
injected as template **data**, never as template source.

## Features

* **Pages from config** — one `[[pages]]` block per page and nav tab.
* **Blog pages** — data-driven posts, preview cards, deep links (`/blog#id`),
  optional per-post image and audio narration.
* **Client-key login** — identity is proven once by a key, then carried by an
  `HttpOnly`/`Secure`/`SameSite` session cookie. `/studio` is 404 to guests.
* **Persistent sessions** — sessions survive a restart via `sessions.json`
  (written `0600`, atomically).
* **Generic contact form** — submissions append to `leads.json` and can be
  emailed to an address set in the config.
* **Reverse-proxy friendly** — speaks plain HTTP; a proxy such as Caddy
  terminates TLS and forwards to the configured port.

## Dependencies

* **Go** 1.26 or newer (built and tested with Go 1.27).
* **github.com/BurntSushi/toml** v1.6.0 — the only third-party Go dependency.
* **jQuery 4.0.0** — bundled in `assets/` and loaded by `head.srv`.
* A local `mail` command (e.g. from the `mailx` package) **only** if you set
  `leads_email`; otherwise it is not used.

## Installation

```sh
# 1. Fetch dependencies (once).
go mod download

# 2. Build.
go build -o srvsite .

# 3. Run it. It listens on the port set in srvsite.toml.
./srvsite
```

Then browse to <http://localhost:7800/>.

### Running behind a reverse proxy

SRVsite listens on plain HTTP. Put a proxy in front of it to terminate TLS.
Example (Caddy), serving SRVsite under a subdirectory:

```
example.com {
	handle /srvsite/* {
		reverse_proxy localhost:7800
	}
}
```

Set `base_path = "/srvsite"` in `srvsite.toml` when you mount the site
under a subdirectory like this, so that links, asset URLs and the AJAX
endpoints all resolve through the prefix. Leave it `""` when serving from
the root.

### Auto-restart while editing

During development, restart on every change to a Go or TOML file:

```sh
ls *.go *.toml | entr -r ./srvsite
```

## Configuration (`srvsite.toml`)

| Key | Meaning |
| --- | --- |
| `port` | TCP port to listen on (default `7800`). |
| `site_title` | Rendered as `{{.SiteTitle}}` (the `<title>` and the logo). |
| `base_path` | Optional URL prefix when served from a subdirectory (e.g. `"/srvtest"`). Leave `""` to serve from `/`. |
| `leads_email` | Where contact-form notifications are emailed. `""` disables email. |
| `[[pages]]` | One block per page: `url`, `name`, `page_head`, `pick_file`, optional `config`. |

The **first** `[[pages]]` block is the homepage; pages appear in the nav in
the order they are listed.

## Client keys (dummy credentials)

`users.json` ships with two **example** users. These keys are intentionally
different from any real deployment and **must be changed** before real use:

| sale_id | license_key | comment |
| --- | --- | --- |
| `DEMO-0001` | `DEMO0001-11111111-22222222-33333333` | example user |
| `DEMO-0002` | `DEMO0002-44444444-55555555-66666666` | example user |

A valid key is exactly **35 characters**. Log in from the homepage; on success
you are redirected to the members page at `/studio`.

> There is **no admin role** and **no built-in user editor** in this version —
> `users.json` is edited by hand. Never commit real keys: `sessions.json`,
> `leads.json` and `*.log` are git-ignored.

## Files

| File | Role |
| --- | --- |
| `srvsite.go` | The whole server. |
| `srvsite.toml` | Port, site title, contact address, pages. |
| `head.srv`, `top.srv`, `nav.srv`, `foot.srv`, `foot.js` | Page chrome. |
| `home.srv`, `studio.srv`, `blog.srv` | Page bodies. |
| `blog.toml`, `blog/` | Blog config and post bodies. |
| `users.json` | License-key records (source of truth for logins). |
| `assets/style.css` | The single source of truth for the colour palette. |
| `assets/jquery-4.0.0.min.js` | jQuery, loaded by `head.srv`. |

## Release version history

* **v0.8** — First open-source release. Generic engine (config-driven pages,
  blog subsystem, client-key login with persistent sessions, generic contact
  form), minimal assets, and a plain-text logo. All branding, media and the
  business-specific admin/license tooling removed.

## License

GPLv2. SRVsite comes with ABSOLUTELY NO WARRANTY; see
<https://www.gnu.org/licenses/old-licenses/gpl-2.0.html>.

*Version 0.8 by Jay Brunet <jay@pjbrunet.com>*
