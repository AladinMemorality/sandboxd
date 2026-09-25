package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"os"
)

func printAdmissionStatus(ctx context.Context, db *sql.DB) error {
	rows, err := db.QueryContext(ctx, `SELECT admission_key,runtime_id,template_id,operation,state,charged FROM cube_admission ORDER BY admission_key`)
	if err != nil {
		return err
	}
	defer rows.Close()
	result := []map[string]any{}
	for rows.Next() {
		var key, id, template, operation, state string
		var charged int
		if err = rows.Scan(&key, &id, &template, &operation, &state, &charged); err != nil {
			return err
		}
		result = append(result, map[string]any{"admission_key": key, "runtime_id": id, "template_id": template, "operation": operation, "state": state, "charged": charged})
	}
	if err = rows.Err(); err != nil {
		return err
	}
	return json.NewEncoder(os.Stdout).Encode(result)
}
