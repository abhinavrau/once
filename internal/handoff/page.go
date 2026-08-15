package handoff

import (
	"html/template"
	"net/http"
)

// Handler serves the one-time page on a mux of its own. The daemon uses Mount
// instead, to add these routes beside the ones it already serves.
func Handler(store *Store) http.Handler {
	mux := http.NewServeMux()
	Mount(mux, store)
	return mux
}

// Mount registers the handoff routes. They live under a short, fixed prefix -
// the prefix is part of the QR payload, and payload length decides whether the
// code fits the terminal - and every refusal, whether the token is wrong,
// expired or already burned, is answered with 410 Gone, so a link that was
// spent looks exactly like a link that never existed.
//
// The routes are dark until the custody flow creates a session, which is why
// the daemon can serve them unconditionally.
func Mount(mux *http.ServeMux, store *Store) {
	mux.HandleFunc("GET "+pathPrefix+"{token}", func(w http.ResponseWriter, r *http.Request) {
		current, ok := store.lookup(r.PathValue("token"))
		if !ok {
			gone(w)
			return
		}
		secureHeaders(w)
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_ = pageTemplate.Execute(w, pageData{
			Kit:        current.Kit,
			Path:       PagePath(current.Token),
			TTLMinutes: int(TTL.Minutes()),
		})
	})

	// Saving in the manager burns nothing and goes nowhere: the submit is a
	// real navigation, which is what makes the phone offer to save, and the
	// redirect lands back on this same page so the screen never changes. Only
	// Done ends the session.
	mux.HandleFunc("POST "+pathPrefix+"{token}/saved", func(w http.ResponseWriter, r *http.Request) {
		current, ok := store.lookup(r.PathValue("token"))
		if !ok {
			gone(w)
			return
		}
		secureHeaders(w)
		http.Redirect(w, r, PagePath(current.Token), http.StatusSeeOther)
	})

	mux.HandleFunc("POST "+pathPrefix+"{token}/done", func(w http.ResponseWriter, r *http.Request) {
		token := r.PathValue("token")
		if _, ok := store.lookup(token); !ok {
			gone(w)
			return
		}
		if err := store.burn(token); err != nil {
			gone(w)
			return
		}
		secureHeaders(w)
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte(donePage))
	})
}

// Helpers

func gone(w http.ResponseWriter) {
	secureHeaders(w)
	http.Error(w, "This link has expired. Start the handoff again from the Once screen.", http.StatusGone)
}

// secureHeaders keeps the page out of caches, histories-by-proxy and referrer
// headers. It carries a secret, so it must not be stored anywhere but the
// password manager the user chooses.
func secureHeaders(w http.ResponseWriter) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Referrer-Policy", "no-referrer")
	w.Header().Set("X-Content-Type-Options", "nosniff")
}

type pageData struct {
	Kit        Kit
	Path       string
	TTLMinutes int
}

// pageTemplate is the prototype's page, which is the artifact the phone test
// passed against: a real password field with autocomplete="new-password" (the
// entire reason a page is served rather than the secret encoded), the recovery
// details beside it, and Done above the fold on a phone screen.
var pageTemplate = template.Must(template.New("page").Parse(`<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<meta name="referrer" content="no-referrer">
<title>Once - Backup password</title>
<style>
  body { font-family: -apple-system, system-ui, sans-serif; background: #14161f;
         color: #e8e8ee; margin: 0; padding: 1.25rem; }
  main { max-width: 26rem; margin: 0 auto; }
  h1 { font-size: 1.2rem; margin: 0 0 .25rem; }
  p  { color: #a0a3b1; font-size: .9rem; line-height: 1.4; margin: .25rem 0 0; }
  form { margin: 1rem 0; }
  label { display: block; font-size: .75rem; color: #a0a3b1; margin: .75rem 0 .25rem; }
  input { width: 100%; box-sizing: border-box; font-size: 1rem; padding: .65rem .75rem;
          border-radius: .5rem; border: 1px solid #34384a; background: #1d2029; color: #e8e8ee; }
  input[name=password] { font-family: ui-monospace, monospace; letter-spacing: .05em; }
  button { width: 100%; margin-top: .9rem; font-size: 1rem; padding: .75rem;
           border-radius: .5rem; border: 0; background: #4a7dff; color: white; font-weight: 600; }
  button.secondary { background: transparent; border: 1px solid #34384a; color: #e8e8ee;
                     margin-top: .75rem; font-weight: 500; }
  .row { display: flex; gap: .5rem; }
  .row input { flex: 1; min-width: 0; }
  button.icon { width: auto; margin: 0; padding: .55rem .9rem; font-size: .85rem;
                font-weight: 500; background: #2a2e3d; color: #e8e8ee;
                border: 1px solid #34384a; }
  .kithead { display: flex; justify-content: space-between; align-items: center;
             color: #e8e8ee; font-weight: 600; margin-bottom: .5rem; }
  .kit { border: 1px solid #34384a; border-radius: .5rem; padding: .6rem .85rem;
         font-size: .8rem; color: #a0a3b1; line-height: 1.5; }
  .kit code { color: #e8e8ee; word-break: break-all; }
  .once { color: #4a7dff; font-weight: 700; letter-spacing: .12em; font-size: .8rem; }
</style>
</head>
<body>
<main>
  <div class="once">ONCE</div>
  <h1>Your backup password</h1>
  <p>Store the password, then tap <strong>Done</strong> to expire this link.
  It expires on its own in {{.TTLMinutes}} min.</p>

  <form method="post" action="{{.Path}}/saved">
    <label for="u">Saved as</label>
    <input id="u" type="text" name="username" value="once backups - {{.Kit.Hostname}}" autocomplete="username" readonly>
    <label for="p">Password</label>
    <div class="row">
      <input id="p" type="password" name="password" value="{{.Kit.Password}}" autocomplete="new-password">
      <button type="button" class="icon" onclick="copyFrom(document.getElementById('p').value, this)">Copy</button>
    </div>
    <button type="submit">Save in password manager</button>
  </form>

  <div class="kit">
    <div class="kithead">Recovery details
      <button type="button" class="icon" onclick="copyFrom(document.getElementById('kitsrc').textContent, this)">Copy</button>
    </div>
    A new machine needs these too - keep them with the password.<br>
    {{range .Kit.Details}}{{.Label}}: <code>{{.Value}}</code><br>
    {{end}}
    <pre id="kitsrc" hidden>{{range .Kit.Details}}{{.Label}}: {{.Value}}
{{end}}</pre>
  </div>

  <form method="post" action="{{.Path}}/done">
    <button type="submit" class="secondary">Done - I've stored the password</button>
  </form>
</main>
<script>
async function copyFrom(text, btn) {
  try {
    await navigator.clipboard.writeText(text);
  } catch (e) {
    const ta = document.createElement('textarea');
    ta.value = text;
    document.body.appendChild(ta);
    ta.select();
    document.execCommand('copy');
    ta.remove();
  }
  const old = btn.textContent;
  btn.textContent = 'OK';
  setTimeout(() => { btn.textContent = old; }, 1200);
}
</script>
</body>
</html>`))

const donePage = `<!doctype html>
<html lang="en"><head><meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<meta name="referrer" content="no-referrer">
<title>Done</title>
<style>body { font-family: -apple-system, system-ui, sans-serif; background: #14161f;
color: #e8e8ee; display: grid; place-items: center; height: 100vh; margin: 0; }
div { text-align: center; } p { color: #a0a3b1; }</style></head>
<body><div><h1>Done</h1><p>This link no longer works. You can close this page<br>
and finish on the server: type the password back there.</p></div></body></html>`
