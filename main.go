/*
Copyright 2020 Anton Gyllenberg <anton@iki.fi>. All rights reserved.

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU General Public License as published by
the Free Software Foundation, either version 3 of the License, or
(at your option) any later version.

This program is distributed in the hope that it will be useful,
but WITHOUT ANY WARRANTY; without even the implied warranty of
MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE.  See the
GNU General Public License for more details.

You should have received a copy of the GNU General Public License
along with this program.  If not, see <http://www.gnu.org/licenses/>.
*/

// +build js

package main

import (
	"context"
	"fmt"
	"runtime"
	"sync"
	"syscall/js"

	cc "github.com/ChizhovVadim/CounterGo/common"
	"github.com/ChizhovVadim/CounterGo/engine"
	"github.com/ChizhovVadim/CounterGo/eval"
	dom "honnef.co/go/js/dom/v2"
)

func main() {
	eng := &simpleEngine{}
	done := make(chan struct{})
	js.Global().Set("chassm", map[string]interface{}{
		"stop": js.FuncOf(func(_ js.Value, _ []js.Value) interface{} {
			close(done)
			return nil
		}),
		"init":   js.FuncOf(eng.Init),
		"aimove": js.FuncOf(eng.AIMove),
		"move":   js.FuncOf(eng.ManualMove),
		"undo": js.FuncOf(func(_ js.Value, _ []js.Value) interface{} {
			eng.mux.Lock()
			defer eng.mux.Unlock()

			if len(eng.positions) > 1 {
				eng.positions = eng.positions[:len(eng.positions)-1]
			}
			return nil
		}),
		"fen": js.FuncOf(func(_ js.Value, _ []js.Value) interface{} {
			eng.mux.Lock()
			defer eng.mux.Unlock()

			return eng.cur().String()
		}),
		"whitesMove": js.FuncOf(func(_ js.Value, _ []js.Value) interface{} {
			eng.mux.Lock()
			defer eng.mux.Unlock()

			return eng.cur().WhiteMove
		}),
		"isOver":    js.FuncOf(eng.IsOver),
		"updateLog": js.FuncOf(eng.UpdateLog),
	})

	<-done
}

type simpleEngine struct {
	mux sync.Mutex
	e   *engine.Engine
	// positions is the sequence of positions in the game
	// positions[0] is the initial position.
	positions []cc.Position
}

func (e *simpleEngine) cur() *cc.Position {
	return &e.positions[len(e.positions)-1]
}

func (e *simpleEngine) Init(_ js.Value, args []js.Value) interface{} {
	e.mux.Lock()
	defer e.mux.Unlock()

	e.e = engine.NewEngine(func() engine.Evaluator {
		return eval.NewEvaluationService()
	})

	var fen string
	if len(args) < 1 {
		fen = cc.InitialPositionFen
	} else {
		fen = args[0].String()
	}

	pos, _ := cc.NewPositionFromFEN(fen)
	e.positions = []cc.Position{pos}

	return fen
}

func (e *simpleEngine) AIMove(_ js.Value, args []js.Value) interface{} {
	e.mux.Lock()
	defer e.mux.Unlock()

	search := cc.SearchParams{}
	search.Positions = e.positions
	search.Limits = cc.LimitsType{
		MoveTime: 300,
	}
	search.Progress = func(si cc.SearchInfo) {
		runtime.Gosched()
	}
	ctx := context.Background()
	info := e.e.Search(ctx, search)
	if len(info.MainLine) == 0 {
		panic("AI breakdown!")
	}
	move := info.MainLine[0]
	newpos := cc.Position{}
	e.cur().MakeMove(move, &newpos)
	e.positions = append(e.positions, newpos)

	return newpos.String()
}

func (e *simpleEngine) ManualMove(_ js.Value, args []js.Value) interface{} {
	e.mux.Lock()
	defer e.mux.Unlock()

	if len(args) != 1 {
		return nil
	}
	newpos, ok := e.cur().MakeMoveLAN(args[0].String())
	if !ok {
		newpos, ok = e.cur().MakeMoveLAN(args[0].String() + "q")
		if !ok {
			return nil
		}
	}
	e.positions = append(e.positions, newpos)

	return newpos.String()
}

func (e *simpleEngine) IsOver(_ js.Value, args []js.Value) interface{} {
	return len(e.cur().GenerateLegalMoves()) == 0
}

func (e *simpleEngine) UpdateLog(_ js.Value, args []js.Value) interface{} {
	if len(args) < 2 {
		return nil
	}

	olid := args[0].String()
	d := dom.GetWindow().Document()
	ol := d.GetElementByID(olid)
	if ol == nil {
		fmt.Println("chassm.UpdateLog:", olid, "not found.")
		return nil
	}

	// Empty OL element
	ol.SetTextContent("")
	var li dom.Element
	for i := 0; i < len(e.positions)-1; i++ {
		move := FAN(e.positions, i)
		span := d.CreateElement("span").(*dom.HTMLSpanElement)
		span.SetTextContent(move)
		if i%2 == 0 {
			// White move begins new list element
			li = d.CreateElement("li")
			span.SetClass("gamelog-whitemove")
			ol.AppendChild(li)
		} else {
			// Black move
			span.SetClass("gamelog-blackmove")
		}
		li.AppendChild(span)
		space := d.CreateElement("span").(*dom.HTMLSpanElement)
		space.SetClass("gamelog-dummyspace")
		space.SetTextContent(" ")
		li.AppendChild(space)
	}

	// Final Score
	txtid := args[1].String()
	txt := d.GetElementByID(txtid)
	if txt == nil {
		fmt.Println("chassm.UpdateLog:", txtid, "not found.")
		return nil
	}

	// Legal moves -> game is not over
	p := e.cur()
	if n := len(p.GenerateLegalMoves()); n > 0 {
		txt.SetTextContent("")
		return nil
	}

	if !p.IsCheck() {
		txt.SetTextContent("½-½")
	} else if !p.WhiteMove {
		txt.SetTextContent("1-0")
	} else {
		txt.SetTextContent("0-1")
	}

	return nil
}

func (e *simpleEngine) movelist() []string {
	rows := make([]string, (len(e.positions))/2)
	for i := 0; i < len(e.positions)-1; i++ {
		if i%2 == 0 {
			// white move
			rows[i/2] = FAN(e.positions, i)
		} else {
			// black move
			rows[i/2] += " " + FAN(e.positions, i)
		}
	}

	return rows
}

var pieceNames map[int]string = map[int]string{
	cc.Knight: "N",
	cc.Bishop: "B",
	cc.Rook:   "R",
	cc.Queen:  "Q",
	cc.King:   "K",
}

func pieceSymbol(white bool, piece int) string {
	var idx int

	switch piece {
	case cc.King:
		idx = 0
	case cc.Queen:
		idx = 1
	case cc.Rook:
		idx = 2
	case cc.Bishop:
		idx = 3
	case cc.Knight:
		idx = 4
	case cc.Pawn:
		idx = 5
	}
	if !white {
		idx += 6
	}
	idx += 9812

	return string(idx)
}

func FAN(pl []cc.Position, halfmove int) string {
	prevpos := pl[halfmove]
	pos := pl[halfmove+1]
	mv := pos.LastMove

	// Check / mate
	checksuffix := func() string {
		if pos.IsCheck() {
			if len(pos.GenerateLegalMoves()) == 0 {
				return "#"
			} else {
				return "†"
			}
		}
		return ""
	}

	piece := mv.MovingPiece()
	if piece == cc.King && cc.File(mv.From()) == cc.FileE {
		if cc.File(mv.To()) == cc.FileG {
			return "0-0" + checksuffix()
		}
		if cc.File(mv.To()) == cc.FileC {
			return "0-0-0" + checksuffix()
		}
	}

	capture := mv.CapturedPiece() != cc.Empty
	white := pl[halfmove].WhiteMove
	fromsq := cc.SquareName(mv.From())
	fromfile := fromsq[0:1]
	fromrank := fromsq[1:2]

	// Piece symbol
	s := ""
	if piece != cc.Pawn {
		s = pieceSymbol(white, piece)
	}

	// From
	if piece == cc.Pawn && capture {
		s = fromfile
	}

	ml := prevpos.GenerateLegalMoves()
	rankmap := map[int]int{}
	filemap := map[int]int{}
	moves := 0
	for i := range ml {
		if ml[i].To() != mv.To() {
			continue
		}
		if ml[i].MovingPiece() != mv.MovingPiece() {
			continue
		}
		if ml[i].Promotion() != mv.Promotion() {
			continue
		}
		moves++
		from := ml[i].From()
		rank := cc.Rank(from)
		file := cc.File(from)
		filemap[file]++
		rankmap[rank]++
	}
	if moves > 1 {
		if len(filemap) == moves {
			s += fromfile
		} else if len(rankmap) == moves {
			s += fromrank
		} else {
			s += fromsq
		}
	}

	// Capture
	if capture {
		s = s + "x"
	}

	// Destination
	s = s + cc.SquareName(mv.To())

	// En passant
	if piece == cc.Pawn && mv.CapturedPiece() == cc.Pawn &&
		prevpos.WhatPiece(mv.To()) == cc.Empty {
		s = s + " e.p."
	}

	// Promotion
	if cp := mv.Promotion(); cp != cc.Empty {
		s += "=" + pieceSymbol(white, cp)
	}

	s += checksuffix()

	return s
}
