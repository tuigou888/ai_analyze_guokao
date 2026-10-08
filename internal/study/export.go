package study

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/csv"
	"errors"
	"strconv"
	"strings"
	"time"
	"unicode"
)

const maxExportRows = 5000

// ExportCSV buffers a bounded snapshot before HTTP headers are sent. A 5,001st
// row rejects the whole export instead of silently truncating an attachment.
func (s *Service) ExportCSV(ctx context.Context, user int64, rangeDays string) ([]byte, error) {
	now := s.clockNow()
	var start time.Time
	end := studyToday(now).AddDate(0, 0, 1)
	if rangeDays != "all" {
		days, err := strconv.Atoi(rangeDays)
		if err != nil || !validStudyDays(days) {
			return nil, ErrInvalid
		}
		start, end = studyWindow(now, days)
	}
	tx, err := s.DB.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	args := []any{user, end.UTC().Format(time.RFC3339)}
	where := `p.user_id=? AND p.submitted_at IS NOT NULL AND julianday(p.submitted_at)<julianday(?)`
	if !start.IsZero() {
		where += ` AND julianday(p.submitted_at)>=julianday(?)`
		args = append(args, start.UTC().Format(time.RFC3339))
	}
	rows, err := tx.QueryContext(ctx, `SELECT p.submitted_at,p.kind,p.id,a.question_id,COALESCE(q.module,''),a.user_answer,a.is_correct,a.duration_ms FROM practice_answer a JOIN practice_session p ON p.id=a.session_id JOIN question q ON q.id=a.question_id WHERE `+where+` ORDER BY julianday(p.submitted_at),p.id,a.ord LIMIT 5001`, args...)
	if err != nil {
		return nil, err
	}
	var buf bytes.Buffer
	buf.WriteString("\ufeff")
	writer := csv.NewWriter(&buf)
	if err = writer.Write([]string{"提交时间（北京时间）", "练习类型", "练习 ID", "题号", "模块", "本人答案", "是否正确", "记录耗时（毫秒）"}); err != nil {
		rows.Close()
		return nil, err
	}
	count := 0
	for rows.Next() {
		count++
		if count > maxExportRows {
			rows.Close()
			return nil, errors.Join(ErrInvalid, errors.New("导出最多 5,000 条记录，请缩小日期范围"))
		}
		var submitted, kind, module, answer string
		var session, question, duration int64
		var correct int
		if err = rows.Scan(&submitted, &kind, &session, &question, &module, &answer, &correct, &duration); err != nil {
			rows.Close()
			return nil, err
		}
		stamp, parseErr := time.Parse(time.RFC3339, submitted)
		if parseErr != nil {
			rows.Close()
			return nil, parseErr
		}
		record := []string{stamp.In(studyZone).Format(time.RFC3339), kind, strconv.FormatInt(session, 10), strconv.FormatInt(question, 10), module, answer, strconv.Itoa(correct), strconv.FormatInt(duration, 10)}
		for i := range record {
			record[i] = safeCSVCell(record[i])
		}
		if err = writer.Write(record); err != nil {
			rows.Close()
			return nil, err
		}
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	writer.Flush()
	if err = writer.Error(); err != nil {
		return nil, err
	}
	if err = tx.Commit(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func safeCSVCell(value string) string {
	trimmed := strings.TrimLeftFunc(value, func(r rune) bool { return unicode.IsSpace(r) || unicode.IsControl(r) || r == '\ufeff' })
	if trimmed != "" && strings.ContainsRune("=+-@", rune(trimmed[0])) {
		return "'" + value
	}
	// Leading tab/CR/LF can themselves trigger spreadsheet interpretation.
	if value != "" && strings.ContainsRune("\t\r\n", rune(value[0])) {
		return "'" + value
	}
	return value
}
