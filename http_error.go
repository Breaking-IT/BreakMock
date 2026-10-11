// Copyright 2026 Breaking-IT. Licensed under Apache-2.0; see LICENSE.
package breakmock

import (
	"net/http"
	"strconv"
	"strings"
)

type wireEntityError struct{ message string }

func (e *wireEntityError) Error() string { return e.message }
func writeJettyError(w http.ResponseWriter, r *http.Request, status int, message string) {
	escape := func(v string) string {
		return strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;", "\"", "&quot;", "'", "&apos;").Replace(v)
	}
	scheme := "http"
	if r.TLS != nil {
		scheme = "https"
	}
	uri := escape(scheme + "://" + r.Host + r.URL.RequestURI())
	msg := escape(message)
	code := strconv.Itoa(status)
	body := "<html>\n<head>\n<meta http-equiv=\"Content-Type\" content=\"text/html;charset=ISO-8859-1\"/>\n<title>Error " + code + " " + msg + "</title>\n</head>\n<body>\n<h2>HTTP ERROR " + code + " " + msg + "</h2>\n<table>\n<tr><th>URI:</th><td>" + uri + "</td></tr>\n<tr><th>STATUS:</th><td>" + code + "</td></tr>\n<tr><th>MESSAGE:</th><td>" + msg + "</td></tr>\n</table>\n\n</body>\n</html>\n"
	w.Header().Set("Content-Type", "text/html;charset=iso-8859-1")
	w.Header().Set("Cache-Control", "must-revalidate,no-cache,no-store")
	w.WriteHeader(status)
	data, _ := entityEncode(body, "ISO-8859-1")
	w.Write(data)
}
