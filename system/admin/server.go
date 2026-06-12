package admin

import (
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/ponzu-cms/ponzu/system/cfg"

	"github.com/ponzu-cms/ponzu/system"
	"github.com/ponzu-cms/ponzu/system/admin/user"
	"github.com/ponzu-cms/ponzu/system/api"
	"github.com/ponzu-cms/ponzu/system/db"
	"github.com/ponzu-cms/ponzu/system/storage"
)

// Run adds Handlers to default http listener for Admin
func Run() {
	http.HandleFunc("/admin", user.Auth(adminHandler))

	http.HandleFunc("/admin/init", initHandler)

	http.HandleFunc("/admin/login", loginHandler)
	http.HandleFunc("/admin/logout", logoutHandler)

	http.HandleFunc("/admin/recover", forgotPasswordHandler)
	http.HandleFunc("/admin/recover/key", recoveryKeyHandler)

	http.HandleFunc("/admin/addons", user.Auth(addonsHandler))
	http.HandleFunc("/admin/addon", user.Auth(addonHandler))

	http.HandleFunc("/admin/configure", user.Auth(configHandler))
	http.HandleFunc("/admin/configure/users", user.Auth(configUsersHandler))
	http.HandleFunc("/admin/configure/users/edit", user.Auth(configUsersEditHandler))
	http.HandleFunc("/admin/configure/users/delete", user.Auth(configUsersDeleteHandler))

	http.HandleFunc("/admin/uploads", user.Auth(uploadContentsHandler))
	http.HandleFunc("/admin/uploads/search", user.Auth(uploadSearchHandler))

	http.HandleFunc("/admin/contents", user.Auth(contentsHandler))
	http.HandleFunc("/admin/contents/search", user.Auth(searchHandler))
	http.HandleFunc("/admin/contents/export", user.Auth(exportHandler))

	http.HandleFunc("/admin/edit", user.Auth(editHandler))
	http.HandleFunc("/admin/edit/delete", user.Auth(deleteHandler))
	http.HandleFunc("/admin/edit/approve", user.Auth(approveContentHandler))
	http.HandleFunc("/admin/edit/upload", user.Auth(editUploadHandler))
	http.HandleFunc("/admin/edit/upload/delete", user.Auth(deleteUploadHandler))

	staticDir := cfg.AdminStaticDir()

	http.Handle("/admin/static/", db.CacheControl(http.StripPrefix("/admin/static", http.FileServer(restrict(http.Dir(staticDir))))))

	// API path needs to be registered within server package so that it is handled
	// even if the API server is not running. Otherwise, images/files uploaded
	// through the editor will not load within the admin system.
	//
	// When S3 object storage is configured, requests to /api/uploads/ are
	// redirected (302) to the S3 object URL. Otherwise files are served from
	// the local upload directory as before.
	s3cfg := storage.ConfigFromEnv()
	if s3cfg.IsConfigured() {
		s3 := storage.NewS3Storage(s3cfg)
		http.HandleFunc("/api/uploads/", func(res http.ResponseWriter, req *http.Request) {
			key := strings.TrimPrefix(req.URL.Path, "/api/uploads/")
			redirectURL := fmt.Sprintf("https://%s/%s", s3.Host(), key)
			http.Redirect(res, req, redirectURL, http.StatusFound)
		})
	} else {
		uploadsDir := cfg.UploadDir()
		http.Handle("/api/uploads/", api.Record(api.CORS(db.CacheControl(http.StripPrefix("/api/uploads/", http.FileServer(restrict(http.Dir(uploadsDir))))))))
	}

	// Database & uploads backup via HTTP route registered with Basic Auth middleware.
	http.HandleFunc("/admin/backup", system.BasicAuth(backupHandler))
}

// Docs adds the documentation file server to the server, accessible at
// http://localhost:1234 by default
func Docs(port int) {
	pwd, err := os.Getwd()
	if err != nil {
		log.Fatalln("Couldn't find current directory for file server.")
	}

	docsDir := filepath.Join(pwd, "docs", "build")

	addr := fmt.Sprintf(":%d", port)
	url := fmt.Sprintf("http://localhost%s", addr)

	fmt.Println("")
	fmt.Println("View documentation offline at:", url)
	fmt.Println("")

	go http.ListenAndServe(addr, http.FileServer(http.Dir(docsDir)))
}
