package admin

import (
	"encoding/csv"
	"fmt"
	"io/ioutil"
	"log"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/ponzu-cms/ponzu/management/format"
	"github.com/ponzu-cms/ponzu/system/backup"
	"github.com/ponzu-cms/ponzu/system/db"
	"github.com/ponzu-cms/ponzu/system/item"

	"github.com/tidwall/gjson"
)

func exportHandler(res http.ResponseWriter, req *http.Request) {
	// /admin/contents/export?type=Blogpost&format=csv
	q := req.URL.Query()
	t := q.Get("type")
	f := strings.ToLower(q.Get("format"))

	if t == "" || f == "" {
		v, err := Error400()
		if err != nil {
			res.WriteHeader(http.StatusInternalServerError)
			return
		}

		res.WriteHeader(http.StatusBadRequest)
		_, err = res.Write(v)
		if err != nil {
			res.WriteHeader(http.StatusInternalServerError)
			return
		}

	}

	pt, ok := item.Types[t]
	if !ok {
		res.WriteHeader(http.StatusBadRequest)
		return
	}

	switch f {
	case "csv":
		csv, ok := pt().(format.CSVFormattable)
		if !ok {
			res.WriteHeader(http.StatusBadRequest)
			return
		}

		fields := csv.FormatCSV()
		exportCSV(res, req, pt, fields)

	default:
		res.WriteHeader(http.StatusBadRequest)
		return
	}
}

func exportCSV(res http.ResponseWriter, req *http.Request, pt func() interface{}, fields []string) {
	tmpFile, err := ioutil.TempFile(os.TempDir(), "exportcsv-")
	if err != nil {
		log.Println("Failed to create tmp file for CSV export:", err)
		res.WriteHeader(http.StatusInternalServerError)
		return
	}
	// Guarantee cleanup even if a later step panics.
	defer os.Remove(tmpFile.Name())

	err = os.Chmod(tmpFile.Name(), 0666)
	if err != nil {
		log.Println("chmod err:", err)
	}

	csvBuf := csv.NewWriter(tmpFile)

	t := req.URL.Query().Get("type")

	// get content data and loop through creating a csv row per result
	bb := db.ContentAll(t)

	// add field names to first row
	err = csvBuf.Write(fields)
	if err != nil {
		res.WriteHeader(http.StatusInternalServerError)
		log.Println("Failed to write column headers:", fields)
		return
	}

	for row := range bb {
		// unmarshal data and loop over fields
		rowBuf := []string{}

		for _, col := range fields {
			// pull out each field as the column value
			result := gjson.GetBytes(bb[row], col)

			// append it to the buffer
			rowBuf = append(rowBuf, result.String())
		}

		// write row to csv
		err := csvBuf.Write(rowBuf)
		if err != nil {
			res.WriteHeader(http.StatusInternalServerError)
			log.Println("Failed to write column headers:", fields)
			return
		}
	}

	csvBuf.Flush()

	tmpFile.Close()

	filename := fmt.Sprintf("export-%s-%d.csv", t, time.Now().Unix())
	dl, err := backup.NewFileDownload(tmpFile.Name(), filename, "text/csv")
	if err != nil {
		log.Println("Failed to prepare CSV download:", err)
		res.WriteHeader(http.StatusInternalServerError)
		return
	}

	if err := dl.Serve(res); err != nil {
		log.Println("Failed to serve CSV download:", err)
	}
}
