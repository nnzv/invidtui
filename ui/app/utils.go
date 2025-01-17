package app

import (
	"fmt"

	inv "github.com/darkhz/invidtui/invidious"
	"github.com/darkhz/tview"
	"github.com/gdamore/tcell/v2"
)

// HorizontalLine returns a box with a thick horizontal line.
func HorizontalLine() *tview.Box {
	return tview.NewBox().
		SetBackgroundColor(tcell.ColorDefault).
		SetDrawFunc(func(
			screen tcell.Screen,
			x, y, width, height int) (int, int, int, int) {
			centerY := y + height/2
			for cx := x; cx < x+width; cx++ {
				screen.SetContent(
					cx,
					centerY,
					tview.BoxDrawingsLightHorizontal,
					nil,
					tcell.StyleDefault.Foreground(tcell.ColorWhite),
				)
			}

			return x + 1,
				centerY + 1,
				width - 2,
				height - (centerY + 1 - y)
		})
}

// VerticalLine returns a box with a thick vertical line.
func VerticalLine() *tview.Box {
	return tview.NewBox().
		SetBackgroundColor(tcell.ColorDefault).
		SetDrawFunc(func(
			screen tcell.Screen,
			x, y, width, height int,
		) (int, int, int, int) {
			for cy := y; cy < y+height; cy++ {
				screen.SetContent(x, cy, tview.BoxDrawingsLightVertical, nil, tcell.StyleDefault.Foreground(tcell.ColorWhite))
				screen.SetContent(x+width-1, cy, tview.BoxDrawingsLightVertical, nil, tcell.StyleDefault.Foreground(tcell.ColorWhite))
			}

			return x, y, width, height
		})
}

// ModifyReference modifies the currently selected entry within the focused table.
func ModifyReference(title string, add bool, info ...inv.SearchData) error {
	err := fmt.Errorf("Application: Cannot modify list entry")

	table := FocusedTable()
	if table == nil {
		return err
	}

	for i := 0; i < table.GetRowCount(); i++ {
		cell := table.GetCell(i, 0)
		if cell == nil {
			continue
		}

		ref := cell.GetReference()
		if ref == nil {
			continue
		}

		if info[0] == ref.(inv.SearchData) {
			if add {
				cell.SetText(title)
				cell.SetReference(info[1])
			} else {
				table.RemoveRow(i)
			}

			break
		}
	}

	return nil
}

// FocusedTableReference returns the currently selected entry's information
// from the focused table.
func FocusedTableReference() (inv.SearchData, error) {
	var table *tview.Table

	err := fmt.Errorf("Application: Cannot select this entry")

	table = FocusedTable()
	if table == nil {
		return inv.SearchData{}, err
	}

	row, _ := table.GetSelection()

	for col := 0; col <= 1; col++ {
		cell := table.GetCell(row, col)
		if cell == nil {
			return inv.SearchData{}, err
		}

		info, ok := cell.GetReference().(inv.SearchData)
		if ok {
			return info, nil
		}
	}

	return inv.SearchData{}, err
}

// FocusedTable returns the currently focused table.
func FocusedTable() *tview.Table {
	item := UI.GetFocus()

	if item, ok := item.(*tview.Table); ok {
		return item
	}

	return nil
}
