package main

import "html/template"

const baseCSS = `
:root{--bg:#f5f6f8;--card:#fff;--text:#1d2330;--muted:#5b6475;--brand:#1E3A8A;--ok:#11734b;--err:#b42318;--line:#e3e6eb}
@media (prefers-color-scheme:dark){:root{--bg:#14171c;--card:#1d2128;--text:#e8eaee;--muted:#a2a9b6;--brand:#7d9cf0;--ok:#4cc38a;--err:#ff8a80;--line:#2c313a}}
*{box-sizing:border-box}body{margin:0;background:var(--bg);color:var(--text);font:16px/1.5 system-ui,-apple-system,"Segoe UI",Roboto,sans-serif}
main{max-width:640px;margin:0 auto;padding:32px 16px}
.card{background:var(--card);border:1px solid var(--line);border-radius:12px;padding:24px;margin-bottom:16px}
h1{font-size:24px;margin:0 0 8px}h2{font-size:18px;margin:0 0 12px}p{margin:0 0 12px}.muted{color:var(--muted);font-size:14px}
.btn{display:block;width:100%;text-align:center;padding:14px 16px;border-radius:10px;border:0;background:var(--brand);color:#fff;font-size:17px;font-weight:600;text-decoration:none;cursor:pointer;margin:8px 0}
.btn.secondary{background:transparent;color:var(--brand);border:2px solid var(--brand)}
.btn.small{display:inline-block;width:auto;padding:6px 12px;font-size:14px;margin:0}
input[type=text],input[type=password]{width:100%;padding:10px 12px;border:1px solid var(--line);border-radius:8px;background:var(--bg);color:var(--text);font-size:16px;margin:4px 0 12px}
label{font-size:14px;color:var(--muted)}.err{color:var(--err);font-weight:600}.ok{color:var(--ok);font-weight:600}
table{width:100%;border-collapse:collapse;font-size:14px}td,th{text-align:left;padding:8px 4px;border-top:1px solid var(--line);vertical-align:top}
code{word-break:break-all;background:var(--bg);padding:8px;border-radius:6px;display:block;margin:8px 0}
ol{padding-left:20px;margin:0 0 12px}
`

var landingTmpl = template.Must(template.New("landing").Parse(`<!doctype html>
<html lang="en"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1">
<title>Manashakti Support</title><style>` + baseCSS + `</style></head><body><main>
<div class="card">
<h1>Manashakti IT Support</h1>
<p><strong>{{.By}}</strong> has invited <strong>{{.Name}}</strong> to get remote help on this computer.</p>
<p class="muted">Only continue if you asked Manashakti IT for help. This link works until {{.Expires}}.</p>
</div>
<div class="card">
<h2>Get help now</h2>
<p>Downloads a small program and starts Manashakti Support. Nothing is installed. Your technician connects after you click <strong>Accept</strong>, and you can end the session at any time.</p>
<a class="btn" href="/i/{{.Token}}/helper/help">Get help now</a>
</div>
{{if .AllowSetup}}
<div class="card">
<h2>Get help now and allow help any time</h2>
<p>Installs Manashakti Support so IT can help this computer later without you being present. A small window shows whenever someone is connected. You can uninstall it at any time from Windows Settings → Apps.</p>
<a class="btn secondary" href="/i/{{.Token}}/helper/setup">Allow help any time</a>
</div>
{{end}}
<div class="card">
<h2>After downloading</h2>
<ol>
<li>Open the downloaded file.</li>
<li>If Windows shows <em>“Windows protected your PC”</em>, click <strong>More info</strong> → <strong>Run anyway</strong>.</li>
<li>For “Allow help any time”, click <strong>Yes</strong> when Windows asks to allow changes.</li>
</ol>
<p class="muted">Windows 10 or 11 is required.</p>
</div>
</main></body></html>`))

var techTmpl = template.Must(template.New("tech").Parse(`<!doctype html>
<html lang="en"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1">
<title>Support Invites</title><style>` + baseCSS + `</style></head><body><main>
<h1>Support invites</h1>
{{if .Error}}<p class="err">{{.Error}}</p>{{end}}
{{if not .User}}
<div class="card"><h2>Sign in</h2>
<form method="post" action="/invites/login">
<label>Username</label><input type="text" name="username" autocomplete="username" required>
<label>Password</label><input type="password" name="password" autocomplete="current-password" required>
<button class="btn" type="submit">Sign in</button></form>
<p class="muted">Use your Manashakti Support technician account.</p></div>
{{else}}
<p class="muted">Signed in as {{.Name}} ({{.User}}).
<form method="post" action="/invites/logout" style="display:inline"><button class="btn small secondary" type="submit">Sign out</button></form></p>
{{if .NewLink}}
<div class="card"><h2 class="ok">Invite for {{.NewName}} created</h2>
<p>Send this link (valid 24 hours). It is shown only once.</p>
<code id="link">{{.NewLink}}</code>
<button class="btn small" type="button" onclick="navigator.clipboard.writeText(document.getElementById('link').textContent);this.textContent='Copied'">Copy link</button>
</div>
{{end}}
<div class="card"><h2>New invite</h2>
<form method="post" action="/invites/create">
<label>Person or PC name</label><input type="text" name="name" maxlength="60" required placeholder="e.g. Reception PC">
<label>Tags for permanent access (comma-separated, optional)</label><input type="text" name="tags" placeholder="e.g. Office">
<label><input type="checkbox" name="allow_setup" checked> Offer “allow help any time” (adds the PC to {{.Book}})</label>
<button class="btn" type="submit">Create invite link</button></form></div>
<div class="card"><h2>Your invites</h2>
{{if .Invites}}<table><tr><th>Name</th><th>Status</th><th>Expires</th><th></th></tr>
{{range .Invites}}<tr><td>{{.Name}}{{if .AllowSetup}}<br><span class="muted">any-time offered</span>{{end}}</td><td>{{.Status}}</td><td>{{.Expires}}</td>
<td>{{if .Active}}<form method="post" action="/invites/revoke"><input type="hidden" name="key" value="{{.Key}}"><button class="btn small secondary" type="submit">Cancel</button></form>{{end}}</td></tr>{{end}}
</table>{{else}}<p class="muted">No invites yet.</p>{{end}}
<p class="muted">PCs that accept an invite appear in the address book {{.Book}} in the app: “Invite: name” for one-time help, or the name itself for permanent access. Refresh the address book to see them.</p>
</div>
{{end}}
</main></body></html>`))
