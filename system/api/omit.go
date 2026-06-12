package api

import (
	"fmt"
	"log"
	"net/http"

	"github.com/ponzu-cms/ponzu/system/item"

	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

func omit(res http.ResponseWriter, req *http.Request, it interface{}, data []byte) ([]byte, error) {
	// is it Omittable
	om, ok := it.(item.Omittable)
	if !ok {
		return data, nil
	}

	return omitFields(res, req, om, data, "data")
}

func omitFields(res http.ResponseWriter, req *http.Request, om item.Omittable, data []byte, pathPrefix string) ([]byte, error) {
	// get fields to omit from json data
	fields, err := om.Omit(res, req)
	if err != nil {
		return nil, err
	}

	// remove each field from json, all responses contain json object(s) in top-level "data" array
	n := int(gjson.GetBytes(data, pathPrefix+".#").Int())
	for i := 0; i < n; i++ {
		for k := range fields {
			var err error
			data, err = sjson.DeleteBytes(data, fmt.Sprintf("%s.%d.%s", pathPrefix, i, fields[k]))
			if err != nil {
				log.Println("Erorr omitting field:", fields[k], "from item.Omittable:", om)
				return nil, err
			}
		}
	}

	return data, nil
}

// omitItem removes a single content type's Omittable fields from a bare JSON
// object (one item, not wrapped in a "data" array). It is used for mixed-type
// result sets — such as a cross-type search response — where each item may be a
// different type and therefore omit a different set of fields. The array-based
// omit above remains for single-type responses.
func omitItem(res http.ResponseWriter, req *http.Request, it interface{}, data []byte) ([]byte, error) {
	om, ok := it.(item.Omittable)
	if !ok {
		return data, nil
	}

	fields, err := om.Omit(res, req)
	if err != nil {
		return nil, err
	}

	for _, field := range fields {
		data, err = sjson.DeleteBytes(data, field)
		if err != nil {
			log.Println("Error omitting field:", field, "from item.Omittable:", om)
			return nil, err
		}
	}

	return data, nil
}
