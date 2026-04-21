/**
 * Licensed Materials - Property of gientech.com
 * (C) Copyright 2026 GienTech Technology Co., Ltd. All rights reserved.
 * 2026-02-04 @author yangwanjin
 */

package exporter

import (
	"fmt"
	"path/filepath"
	"time"

	"github.com/xuri/excelize/v2"
	"k8s.io/klog/v2"

	config "hybrid/config/collector"
	"hybrid/pkg/constants"
	"hybrid/pkg/simple/prometheus"
)

// exportToExcel export metrics data to local file(excel)
func (e *Exporter) exportToExcel(allResults map[string][]prometheus.QueryResult) (string, error) {
	// generate filename
	filename := generateExcelFilename(e.config.Export.LocalConfig.Format)
	exportPath := filepath.Join(e.config.Export.LocalConfig.OutputDir, filename)

	// create a new file
	excelFile := excelize.NewFile()
	defer excelFile.Close()

	// delete default sheet "Sheet1"
	err := excelFile.DeleteSheet("Sheet1")
	if err != nil {
		klog.Errorf("Failed to delete default sheet(Sheet1): %v", err)
		return "", err
	}

	// create sheet for each query
	for i, queryConfig := range e.config.Queries {
		results, ok := allResults[queryConfig.Name]
		if !ok {
			continue
		}

		// create sheet
		sheetName := queryConfig.SheetName
		index, err := excelFile.NewSheet(sheetName)
		if err != nil {
			klog.Errorf("Failed to create sheet %s: %v", sheetName, err)
			continue
		}

		// set active sheet, if it's the first sheet
		if i == 0 {
			excelFile.SetActiveSheet(index)
		}

		// write query results to sheet
		if err := e.writeSheet(excelFile, sheetName, queryConfig, results); err != nil {
			klog.Errorf("Failed to write sheet %s: %v", sheetName, err)
			continue
		}

		klog.Infof("Successfully wrote %d rows to sheet %s", len(results), sheetName)
	}

	// save result file
	if err := excelFile.SaveAs(exportPath); err != nil {
		return "", fmt.Errorf("failed to save result file: %w", err)
	}

	klog.Infof("Successfully exported metrics data to file: %s", exportPath)
	return exportPath, nil
}

// writeSheet write query results to sheet
func (e *Exporter) writeSheet(f *excelize.File, sheetName string, queryConfig config.QueryConfig, results []prometheus.QueryResult) error {
	// set header style
	headerStyle, err := f.NewStyle(&excelize.Style{
		Font: &excelize.Font{
			Bold: true,
			Size: 11,
		},
		Fill: excelize.Fill{
			Type:    "pattern",
			Color:   []string{"#E0E0E0"},
			Pattern: 1,
		},
		Alignment: &excelize.Alignment{
			Horizontal: "center",
			Vertical:   "center",
		},
	})
	if err != nil {
		return fmt.Errorf("create header style: %w", err)
	}

	// prepare headers
	headers := []string{"Timestamp"}

	// add label columns
	if len(queryConfig.Labels) > 0 {
		headers = append(headers, queryConfig.Labels...)
	} else if len(results) > 0 {
		// add label columns from first result
		for label := range results[0].Metric {
			headers = append(headers, label)
		}
	}

	// add value column
	headers = append(headers, queryConfig.ValueColumn)

	// write headers to sheet
	for col, header := range headers {
		cell, _ := excelize.CoordinatesToCellName(col+1, 1)
		f.SetCellValue(sheetName, cell, header)
		f.SetCellStyle(sheetName, cell, cell, headerStyle)
	}

	// set column width
	// Timestamp column
	f.SetColWidth(sheetName, "A", "A", 20)
	for i := 1; i < len(headers); i++ {
		col, _ := excelize.ColumnNumberToName(i + 1)
		f.SetColWidth(sheetName, col, col, 15)
	}

	// write data rows
	row := 2
	for _, result := range results {
		for _, tv := range result.Values {
			// write timestamp
			cell, _ := excelize.CoordinatesToCellName(1, row)
			f.SetCellValue(sheetName, cell, tv.Timestamp.Format("2006-01-02 15:04:05"))

			// write label columns
			col := 2
			if len(queryConfig.Labels) > 0 {
				for _, label := range queryConfig.Labels {
					cell, _ := excelize.CoordinatesToCellName(col, row)
					f.SetCellValue(sheetName, cell, result.Metric[label])
					col++
				}
			} else {
				for _, label := range headers[1 : len(headers)-1] {
					cell, _ := excelize.CoordinatesToCellName(col, row)
					f.SetCellValue(sheetName, cell, result.Metric[label])
					col++
				}
			}

			// write metric value column
			cell, _ = excelize.CoordinatesToCellName(col, row)
			f.SetCellValue(sheetName, cell, tv.Value)

			row++
		}
	}

	return nil
}

// generateExcelFilename generate filename for export
func generateExcelFilename(format string) string {
	now := time.Now()
	switch format {
	case "daily":
		return fmt.Sprintf("%s_%s.xlsx", constants.ExportFilePrefix, now.Format("2006-01-02"))
	case "timestamp":
		return fmt.Sprintf("%s_%s.xlsx", constants.ExportFilePrefix, now.Format("20060102_150405"))
	default:
		return fmt.Sprintf("%s_%s.xlsx", constants.ExportFilePrefix, now.Format("20060102_150405"))
	}
}
