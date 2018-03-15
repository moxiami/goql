package executor

import (
	"fmt"
	"strings"

	"github.com/fzerorubigd/goql/astdata"
	"github.com/fzerorubigd/goql/internal/parse"
	"github.com/fzerorubigd/goql/structures"
)

type context struct {
	src    string
	pk     string
	pkg    *astdata.Package
	q      parse.Query
	fields []string
	show   []int
	where  parse.Stack
}

const itemColumn parse.ItemType = -999

// A hack to handle column, I don't like this kind of hacks but I'm too bored :)
type col struct {
	field string
	index int
}

func (col) Type() parse.ItemType {
	return itemColumn
}

// Pos is the position of the column in the requested index
func (c col) Pos() int {
	return c.index
}

func (c col) Value() string {
	return c.field
}

func (c col) String() string {
	return ""
}

// Execute the query
func Execute(p, src string) ([][]interface{}, error) {
	var err error
	ctx := &context{src: src, pk: p}
	ctx.pkg, err = astdata.ParsePackage(p)
	if err != nil {
		return nil, err
	}
	ctx.q, err = parse.AST(ctx.src)
	if err != nil {
		return nil, err
	}
	ss := ctx.q.Statement.(*parse.SelectStmt)

	tbl, err := structures.GetTable(ss.Table)
	if err != nil {
		return nil, err
	}

	// which columns we should select?
	m := make(map[string]int)
	pos := 0
	ctx.show = make([]int, len(ss.Fields))
	for i := range ss.Fields {
		if ss.Fields[i].WildCard {
			// all column, no join support so ignore the rest
			m = make(map[string]int)
			ctx.show = make([]int, len(tbl))
			pos = 0
			for j := range tbl {
				m[j] = pos
				ctx.show[pos] = pos
				pos++
			}
			break
		}
		if ss.Fields[i].Table != "" && ss.Fields[i].Table != ss.Table {
			return nil, fmt.Errorf("table %s is not in select, join is not supported", ss.Fields[i].Table)
		}
		_, ok := tbl[ss.Fields[i].Column]
		if !ok {
			return nil, fmt.Errorf("field %s is not available in table %s", ss.Fields[i].Column, ss.Table)
		}
		if idx, ok := m[ss.Fields[i].Column]; ok {
			// already added
			ctx.show[i] = idx
			continue
		}
		m[ss.Fields[i].Column] = pos
		ctx.show[i] = pos
		pos++
	}

	ctx.where = parse.NewStack(0)
	// which column are needed in where?
	if st := ss.Where; st != nil {
		for {
			p, err := st.Pop()
			if err != nil {
				break
			}
			ts := p
			switch p.Type() {
			case parse.ItemAlpha:
				// this mus be a column name
				v := strings.ToLower(p.Value())
				if v != "null" && v != "true" && v != "false" {
					_, ok := tbl[v]
					if !ok {
						return nil, fmt.Errorf("field %s not found", v)
					}
					if _, ok := m[v]; !ok {
						m[v] = pos
						pos++
					}
					ts = col{
						index: m[v],
						field: v,
					}
				}
			case parse.ItemLiteral2:
				v := parse.GetTokenString(p)
				_, ok := tbl[v]
				if !ok {
					return nil, fmt.Errorf("field %s not found", v)
				}
				if _, ok := m[v]; !ok {
					m[v] = pos
					pos++
				}
				ts = col{
					index: m[v],
					field: v,
				}
			}
			ctx.where.Push(ts)
		}
	}

	ctx.fields = make([]string, len(m))
	for i := range m {
		ctx.fields[m[i]] = i
	}

	return doQuery(ctx)
}

func filterColumn(show []int, items ...interface{}) []interface{} {
	row := make([]interface{}, len(show))
	for idx := range show {
		row[idx] = items[show[idx]]
	}

	return row
}

func callWhere(where getter, i []interface{}) (ok bool, err error) {
	defer func() {
		if e := recover(); e != nil {
			err = fmt.Errorf("error : %v", e)
			ok = false
		}
	}()
	return toBool(where(i)), nil
}

func doQuery(ctx *context) ([][]interface{}, error) {
	res := make(chan []interface{}, 3)
	err := structures.GetFields(ctx.pkg, ctx.q.Statement.(*parse.SelectStmt).Table, res, ctx.fields...)
	if err != nil {
		return nil, err
	}
	where, err := buildFilter(ctx.where)
	if err != nil {
		return nil, err
	}
	a := make([][]interface{}, 0)
	for i := range res {
		ok, err := callWhere(where, i)
		if err != nil {
			return nil, err
		}
		if !ok {
			continue
		}
		a = append(a, filterColumn(ctx.show, i...))
	}

	return a, nil
}