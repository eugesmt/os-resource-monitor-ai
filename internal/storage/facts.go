package storage

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"taskmanager-ai/internal/domain"
)

var factCategories = map[string]bool{"goal": true, "constraint": true, "preference": true, "decision": true, "agreement": true, "parameter": true}

func (s *ChatHistory) ApplyFactMutations(ctx context.Context, scope domain.ContextScope, sourceMessageID int64, mutations []domain.FactMutation) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := applyFactMutationsTx(ctx, tx, scope, sourceMessageID, mutations); err != nil {
		return err
	}
	return tx.Commit()
}

func applyFactMutationsTx(ctx context.Context, tx *sql.Tx, scope domain.ContextScope, sourceMessageID int64, mutations []domain.FactMutation) error {
	now := time.Now().UTC().Format(time.RFC3339Nano)
	var err error
	for _, mutation := range mutations {
		key := normalizeFactKey(mutation.Key)
		if key == "" || len([]rune(key)) > 100 || !factCategories[mutation.Category] {
			return errors.New("invalid fact mutation")
		}
		if mutation.Operation == "noop" {
			continue
		}
		var id, version int64
		var oldValue, state string
		err = tx.QueryRowContext(ctx, `SELECT id,version,COALESCE(value_text,''),state FROM dialogue_facts WHERE branch_id=? AND fact_key=?`, scope.BranchID, key).Scan(&id, &version, &oldValue, &state)
		exists := err == nil
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		if mutation.ExpectedVersion != nil && exists && version != *mutation.ExpectedVersion {
			return fmt.Errorf("fact %s version conflict", key)
		}
		op := mutation.Operation
		newValue := mutation.Value
		newState := state
		switch mutation.Operation {
		case "upsert":
			newState = "active"
			if exists && state != "active" {
				op = "reactivate"
			}
		case "invalidate":
			if !exists {
				continue
			}
			newState = "invalidated"
			newValue = oldValue
		case "conflict":
			if !exists {
				continue
			}
			newState = "conflicted"
			newValue = oldValue
		default:
			return errors.New("invalid fact operation")
		}
		if exists {
			if _, err = tx.ExecContext(ctx, `UPDATE dialogue_facts SET category=?,value_text=?,state=?,version=version+1,updated_at=?,source_message_id=? WHERE id=?`, mutation.Category, newValue, newState, now, sourceMessageID, id); err != nil {
				return err
			}
		} else {
			res, e := tx.ExecContext(ctx, `INSERT INTO dialogue_facts(conversation_id,branch_id,fact_key,category,value_text,state,source_message_id,version,updated_at) VALUES(?,?,?,?,?,?,?,?,?)`, scope.ConversationID, scope.BranchID, key, mutation.Category, newValue, newState, sourceMessageID, 1, now)
			if e != nil {
				return e
			}
			id, _ = res.LastInsertId()
			version = 1
			op = "create"
		}
		var old any
		if exists {
			old = oldValue
		}
		if _, err = tx.ExecContext(ctx, `INSERT INTO dialogue_fact_revisions(fact_id,operation,old_value_text,new_value_text,source_message_id,created_at) VALUES(?,?,?,?,?,?)`, id, op, old, newValue, sourceMessageID, now); err != nil {
			return err
		}
	}
	return nil
}

func ValidateFactValue(value string, maxRunes int) error {
	if len([]rune(strings.TrimSpace(value))) > maxRunes {
		return errors.New("fact value exceeds limit")
	}
	return nil
}
