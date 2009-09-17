// Copyright 2009 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// The printer package implements printing of AST nodes.
package printer

import (
	"container/vector";
	"fmt";
	"go/ast";
	"go/token";
	"io";
	"os";
	"reflect";
	"strings";
	"tabwriter";
)


const (
	debug = false;  // enable for debugging
	maxNewlines = 3;  // maximum vertical white space
)


// Printing is controlled with these flags supplied
// to Fprint via the mode parameter.
//
const (
	GenHTML uint = 1 << iota;  // generate HTML
	RawFormat;  // do not use a tabwriter; if set, UseSpaces is ignored
	UseSpaces;  // use spaces instead of tabs for indentation and alignment
)


type whiteSpace int

const (
	blank = whiteSpace(' ');
	tab = whiteSpace('\t');
	newline = whiteSpace('\n');
	formfeed = whiteSpace('\f');
)


var (
	tabs = [...]byte{'\t', '\t', '\t', '\t', '\t', '\t', '\t', '\t'};
	newlines = [...]byte{'\n', '\n', '\n', '\n', '\n', '\n', '\n', '\n'};  // more than maxNewlines
	ampersand = strings.Bytes("&amp;");
	lessthan = strings.Bytes("&lt;");
	greaterthan = strings.Bytes("&gt;");
)


// A lineTag is a token.Position that is used to print
// line tag id's of the form "L%d" where %d stands for
// the line indicated by position.
//
type lineTag token.Position


// A htmlTag specifies a start and end tag.
type htmlTag struct {
	start, end string;  // empty if tags are absent
}


type printer struct {
	// configuration (does not change after initialization)
	output io.Writer;
	mode uint;
	errors chan os.Error;

	// current state (changes during printing)
	written int;  // number of bytes written
	level int;  // function nesting level; 0 = package scope, 1 = top-level function scope, etc.
	indent int;  // current indentation
	last token.Position;  // (possibly estimated) position immediately after the last item; in AST space
	pos token.Position;  // (possibly estimated) position; in AST space
	tag htmlTag;  // tag to be used around next item
	lastTaggedLine int;  // last line for which a line tag was written

	// buffered whitespace
	buffer [8]whiteSpace;  // whitespace sequences are short (1 or 2); 8 entries is plenty
	buflen int;

	// comments
	comment *ast.CommentGroup;  // list of comments; or nil
}


func (p *printer) init(output io.Writer, mode uint) {
	p.output = output;
	p.mode = mode;
	p.errors = make(chan os.Error);
}


// Writing to p.output is done with write0 which also handles errors.
// Does not indent after newlines, or HTML-escape, or update p.pos.
//
func (p *printer) write0(data []byte) {
	n, err := p.output.Write(data);
	p.written += n;
	if err != nil {
		p.errors <- err;
	}
}


func (p *printer) write(data []byte) {
	i0 := 0;
	for i, b := range data {
		switch b {
		case '\n', '\f':
			// write segment ending in b followed by indentation
			if p.mode & RawFormat != 0 && b == '\f' {
				// no tabwriter - convert last byte into a newline
				p.write0(data[i0 : i]);
				p.write0(newlines[0 : 1]);
			} else {
				p.write0(data[i0 : i+1]);
			}

			// write indentation
			// TODO(gri) should not write indentation if there is nothing else
			//           on the line
			j := p.indent;
			for ; j > len(tabs); j -= len(tabs) {
				p.write0(&tabs);
			}
			p.write0(tabs[0 : j]);

			// update p.pos
			p.pos.Offset += i+1 - i0 + p.indent;
			p.pos.Line++;
			p.pos.Column = p.indent + 1;

			// next segment start
			i0 = i+1;

		case '&', '<', '>':
			if p.mode & GenHTML != 0 {
				// write segment ending in b
				p.write0(data[i0 : i]);

				// write HTML-escaped b
				var esc []byte;
				switch b {
				case '&': esc = ampersand;
				case '<': esc = lessthan;
				case '>': esc = greaterthan;
				}
				p.write0(esc);

				// update p.pos
				p.pos.Offset += i+1 - i0;
				p.pos.Column += i+1 - i0;

				// next segment start
				i0 = i+1;
			}
		}
	}

	// write remaining segment
	p.write0(data[i0 : len(data)]);

	// update p.pos
	n := len(data) - i0;
	p.pos.Offset += n;
	p.pos.Column += n;
}


func (p *printer) writeNewlines(n int) {
	if n > 0 {
		if n > maxNewlines {
			n = maxNewlines;
		}
		p.write(newlines[0 : n]);
	}
}


func (p *printer) writeItem(pos token.Position, data []byte, setLineTag bool) {
	p.pos = pos;
	if debug {
		// do not update p.pos - use write0
		p.write0(strings.Bytes(fmt.Sprintf("[%d:%d]", pos.Line, pos.Column)));
	}
	if p.mode & GenHTML != 0 {
		// no html-escaping and no p.pos update for tags - use write0
		if setLineTag && pos.Line > p.lastTaggedLine {
			// id's must be unique within a document: set
			// line tag only if line number has increased
			// (note: for now write complete start and end
			// tag - shorter versions seem to have issues
			// with Safari)
			p.tag.start = fmt.Sprintf(`<a id="L%d"></a>`, pos.Line);
			p.lastTaggedLine = pos.Line;
		}
		// write start tag, if any
		if p.tag.start != "" {
			p.write0(strings.Bytes(p.tag.start));
			p.tag.start = "";  // tag consumed
		}
		p.write(data);
		// write end tag, if any
		if p.tag.end != "" {
			p.write0(strings.Bytes(p.tag.end));
			p.tag.end = "";  // tag consumed
		}
	} else {
		p.write(data);
	}
	p.last = p.pos;
}


// TODO(gri) decide if this is needed - keep around for now
/*
// Reduce contiguous sequences of '\t' in a []byte to a single '\t'.
func untabify(src []byte) []byte {
	dst := make([]byte, len(src));
	j := 0;
	for i, c := range src {
		if c != '\t' || i == 0 || src[i-1] != '\t' {
			dst[j] = c;
			j++;
		}
	}
	return dst[0 : j];
}
*/


func (p *printer) writeComment(comment *ast.Comment) {
	// separation from last item
	if p.last.IsValid() {
		// there was a preceding item (otherwise, the comment is the
		// first item to be printed - in that case do not apply extra
		// spacing)
		n := comment.Pos().Line - p.last.Line;
		if n == 0 {
			// comment on the same line as last item; separate with tab
			p.write(tabs[0 : 1]);
		} else {
			// comment on a different line; separate with newlines
			p.writeNewlines(n);
		}
	}

	// write comment
	p.writeItem(comment.Pos(), comment.Text, false);
}


func (p *printer) intersperseComments(next token.Position) {
	firstLine := 0;
	needsNewline := false;
	for ; p.comment != nil && p.comment.List[0].Pos().Offset < next.Offset; p.comment = p.comment.Next {
		for _, c := range p.comment.List {
			if firstLine == 0 {
				firstLine = c.Pos().Line;
			}
			p.writeComment(c);
			needsNewline = c.Text[1] == '/';
		}
	}

	// Eliminate non-newline whitespace from whitespace buffer.
	j := 0;
	for i := 0; i < p.buflen; i++ {
		ch := p.buffer[i];
		if ch == '\n' || ch == '\f' {
			p.buffer[j] = ch;
			j++;
		}
	}
	p.buflen = j;

	// Eliminate extra newlines from whitespace buffer if they
	// are not present in the original source. This makes sure
	// that comments that need to be adjacent to a declaration
	// remain adjacent.
	if p.last.IsValid() {
		n := next.Line - p.last.Line;
		if n < p.buflen {
			p.buflen = n;
		}
	}

	// If the whitespace buffer is not empty, it contains only
	// newline or formfeed chars. Force a formfeed char if the
	// comments span more than one line - in this case the
	// structure of the next line is likely to change. Otherwise
	// use the existing char, if any.
	if needsNewline {
		ch := p.buffer[0];  // existing char takes precedence
		if p.buflen == 0 {
			p.buflen = 1;
			ch = newline;  // original ch was a lie
		}
		if p.last.Line > firstLine {
			ch = formfeed;  // comments span at least 2 lines
		}
		p.buffer[0] = ch;
	}
}


func (p *printer) writeWhitespace() {
	var a [len(p.buffer)]byte;
	for i := 0; i < p.buflen; i++ {
		a[i] = byte(p.buffer[i]);
	}

	var b []byte = &a;
	b = b[0 : p.buflen];
	p.buflen = 0;

	p.write(b);
}


// print prints a list of "items" (roughly corresponding to syntactic
// tokens, but also including whitespace and formatting information).
// It is the only print function that should be called directly from
// any of the AST printing functions below.
//
// Whitespace is accumulated until a non-whitespace token appears. Any
// comments that need to appear before that token are printed first,
// taking into account the amount and structure of any pending white-
// space for best comment placement. Then, any leftover whitespace is
// printed, followed by the actual token.
//
func (p *printer) print(args ...) {
	setLineTag := false;
	v := reflect.NewValue(args).(*reflect.StructValue);
	for i := 0; i < v.NumField(); i++ {
		f := v.Field(i);

		next := p.pos;  // estimated position of next item
		var data []byte;
		switch x := f.Interface().(type) {
		case int:
			// indentation delta
			p.indent += x;
			if p.indent < 0 {
				panic("print: negative indentation");
			}
		case whiteSpace:
			if p.buflen >= len(p.buffer) {
				// Whitespace sequences are very short so this should
				// never happen. Handle gracefully (but possibly with
				// bad comment placement) if it does happen.
				p.writeWhitespace();
			}
			p.buffer[p.buflen] = x;
			p.buflen++;
		case []byte:
			data = x;
		case string:
			data = strings.Bytes(x);
		case token.Token:
			data = strings.Bytes(x.String());
		case token.Position:
			if x.IsValid() {
				next = x;  // accurate position of next item
			}
		case lineTag:
			pos := token.Position(x);
			if pos.IsValid() {
				next = pos;  // accurate position of next item
				setLineTag = true;
			}
		case htmlTag:
			p.tag = x;  // tag surrounding next item
		default:
			panicln("print: unsupported argument type", f.Type().String());
		}
		p.pos = next;

		if data != nil {
			p.flush(next);

			// intersperse extra newlines if present in the source
			p.writeNewlines(next.Line - p.pos.Line);

			p.writeItem(next, data, setLineTag);
			setLineTag = false;
		}
	}
}


// Flush prints any pending comments and whitespace occuring
// textually before the position of the next item.
//
func (p *printer) flush(next token.Position) {
	// if there are comments before the next item, intersperse them
	if p.comment != nil && p.comment.List[0].Pos().Offset < next.Offset {
		p.intersperseComments(next);
	}

	p.writeWhitespace();
}


// ----------------------------------------------------------------------------
// Printing of common AST nodes.

// TODO(gri) The code for printing lead and line comments
//           should be eliminated in favor of reusing the
//           comment intersperse mechanism above somehow.

// Print a list of individual comments.
func (p *printer) commentList(list []*ast.Comment) {
	for i, c := range list {
		t := c.Text;
		p.print(c.Pos(), t);
		if t[1] == '/' && i+1 < len(list) {
			//-style comment which is not at the end; print a newline
			p.print(newline);
		}
	}
}


// Print a lead comment followed by a newline.
func (p *printer) leadComment(d *ast.CommentGroup) {
	// Ignore the comment if we have comments interspersed (p.comment != nil).
	if p.comment == nil && d != nil {
		p.commentList(d.List);
		p.print(newline);
	}
}


// Print a tab followed by a line comment.
// A newline must be printed afterwards since
// the comment may be a //-style comment.
func (p *printer) lineComment(d *ast.CommentGroup) {
	// Ignore the comment if we have comments interspersed (p.comment != nil).
	if p.comment == nil && d != nil {
		p.print(tab);
		p.commentList(d.List);
	}
}


func (p *printer) identList(list []*ast.Ident) {
	// convert into an expression list
	xlist := make([]ast.Expr, len(list));
	for i, x := range list {
		xlist[i] = x;
	}
	p.exprList(xlist, commaSep);
}


func (p *printer) stringList(list []*ast.BasicLit) {
	// convert into an expression list
	xlist := make([]ast.Expr, len(list));
	for i, x := range list {
		xlist[i] = x;
	}
	p.exprList(xlist, 0);
}


type exprListMode uint;
const (
	blankStart exprListMode = 1 << iota;  // print a blank before the list
	commaSep;  // elements are separated by commas
	commaTerm;  // elements are terminated by comma
)


// Print a list of expressions. If the list spans multiple
// source lines, the original line breaks are respected.
func (p *printer) exprList(list []ast.Expr, mode exprListMode) {
	if len(list) == 0 {
		return;
	}

	if list[0].Pos().Line == list[len(list)-1].Pos().Line {
		// all list entries on a single line
		if mode & blankStart != 0 {
			p.print(blank);
		}
		for i, x := range list {
			if i > 0 {
				if mode & commaSep != 0 {
					p.print(token.COMMA);
				}
				p.print(blank);
			}
			p.expr(x);
		}
		return;
	}

	// list entries span multiple lines;
	// use source code positions to guide line breaks
	p.print(+1, formfeed);
	line := list[0].Pos().Line;
	for i, x := range list {
		prev := line;
		line = x.Pos().Line;
		if i > 0 {
			if mode & commaSep != 0 {
				p.print(token.COMMA);
			}
			if prev < line {
				p.print(newline);
			} else {
				p.print(blank);
			}
		}
		p.expr(x);
	}
	if mode & commaTerm != 0 {
		p.print(token.COMMA);
	}
	p.print(-1, formfeed);
}


func (p *printer) parameters(list []*ast.Field) {
	p.print(token.LPAREN);
	if len(list) > 0 {
		p.level++;  // adjust nesting level for parameters
		for i, par := range list {
			if i > 0 {
				p.print(token.COMMA, blank);
			}
			p.identList(par.Names);  // p.level > 0; all identifiers will be printed
			if len(par.Names) > 0 {
				// at least one identifier
				p.print(blank);
			};
			p.expr(par.Type);
		}
		p.level--;
	}
	p.print(token.RPAREN);
}


// Returns true if a separating semicolon is optional.
func (p *printer) signature(params, result []*ast.Field) (optSemi bool) {
	p.parameters(params);
	if result != nil {
		p.print(blank);

		if len(result) == 1 && result[0].Names == nil {
			// single anonymous result; no ()'s unless it's a function type
			f := result[0];
			if _, isFtyp := f.Type.(*ast.FuncType); !isFtyp {
				optSemi = p.expr(f.Type);
				return;
			}
		}

		p.parameters(result);
	}
	return;
}


func separator(useTab bool) whiteSpace {
	if useTab {
		return tab;
	}
	return blank;
}


func (p *printer) fieldList(lbrace token.Position, list []*ast.Field, rbrace token.Position, isIncomplete, isStruct bool) {
	if len(list) == 0 && !isIncomplete {
		// no blank between keyword and {} in this case
		// TODO(gri): This will not look nice if there are comments inside the {}'s.
		p.print(lbrace, token.LBRACE, rbrace, token.RBRACE);
		return;
	}

	// at least one entry or incomplete
	p.print(blank, lbrace, token.LBRACE, +1, formfeed);
	if isStruct {
		sep := separator(len(list) > 1);
		for i, f := range list {
			p.leadComment(f.Doc);
			if len(f.Names) > 0 {
				p.identList(f.Names);
				p.print(sep);
			}
			p.expr(f.Type);
			if f.Tag != nil {
				p.print(sep);
				p.expr(&ast.StringList{f.Tag});
			}
			p.print(token.SEMICOLON);
			p.lineComment(f.Comment);
			if i+1 < len(list) || isIncomplete {
				p.print(newline);
			}
		}
		if isIncomplete {
			p.print("// contains unexported fields");
		}
	} else { // interface
		for i, f := range list {
			p.leadComment(f.Doc);
			p.identList(f.Names);
			if len(f.Names) > 1 {
				p.print(blank);
			}
			if ftyp, isFtyp := f.Type.(*ast.FuncType); isFtyp {
				// method(s)
				p.signature(ftyp.Params, ftyp.Results);
			} else {
				// embedded interface
				p.expr(f.Type);
			}
			p.print(token.SEMICOLON);
			p.lineComment(f.Comment);
			if i+1 < len(list) || isIncomplete {
				p.print(newline);
			}
		}
		if isIncomplete {
			p.print("// contains unexported methods");
		}
	}
	p.print(-1, formfeed, rbrace, token.RBRACE);
}


// ----------------------------------------------------------------------------
// Expressions

func needsBlanks(expr ast.Expr) bool {
	switch x := expr.(type) {
	case *ast.Ident:
		// "long" identifiers look better with blanks around them
		return len(x.Value) > 12;  // adjust as looks best
	case *ast.BasicLit:
		// "long" literals look better with blanks around them
		return len(x.Value) > 6;  // adjust as looks best
	case *ast.ParenExpr:
		// parenthesized expressions don't need blanks around them
		return false;
	case *ast.CallExpr:
		// call expressions need blanks if they have more than one
		// argument or if the function or the argument need blanks
		return len(x.Args) > 1 || needsBlanks(x.Fun) || len(x.Args) == 1 && needsBlanks(x.Args[0]);
	}
	return true;
}


// TODO(gri) Write this recursively; get rid of vector use.
func (p *printer) binaryExpr(x *ast.BinaryExpr, prec1 int) {
	prec := x.Op.Precedence();
	if prec < prec1 {
		// parenthesis needed
		// Note: The parser inserts an ast.ParenExpr node; thus this case
		//       can only occur if the AST is created in a different way.
		p.print(token.LPAREN);
		p.expr(x);
		p.print(token.RPAREN);
		return;
	}

	// Traverse left, collect all operations at same precedence
	// and determine if blanks should be printed.
	//
	// This algorithm assumes that the right-hand side of a binary
	// operation has a different (higher) precedence then the current
	// node, which is how the parser creates the AST.
	var list vector.Vector;
	printBlanks := prec <= token.EQL.Precedence() || needsBlanks(x.Y);
	for {
		list.Push(x);
		if t, ok := x.X.(*ast.BinaryExpr); ok && t.Op.Precedence() == prec {
			x = t;
			if needsBlanks(x.Y) {
				printBlanks = true;
			}
		} else {
			break;
		}
	}
	if needsBlanks(x.X) {
		printBlanks = true;
	}

	// Print collected operations left-to-right, with blanks if necessary.
	p.expr1(x.X, prec);
	for list.Len() > 0 {
		x = list.Pop().(*ast.BinaryExpr);
		if printBlanks {
			p.print(blank, x.OpPos, x.Op, blank);
		} else {
			p.print(x.OpPos, x.Op);
		}
		p.expr1(x.Y, prec);
	}
}


// Returns true if a separating semicolon is optional.
func (p *printer) expr1(expr ast.Expr, prec1 int) (optSemi bool) {
	p.print(expr.Pos());

	switch x := expr.(type) {
	case *ast.BadExpr:
		p.print("BadExpr");

	case *ast.Ident:
		p.print(x.Value);

	case *ast.BinaryExpr:
		p.binaryExpr(x, prec1);

	case *ast.KeyValueExpr:
		p.expr(x.Key);
		p.print(x.Colon, token.COLON, blank);
		p.expr(x.Value);

	case *ast.StarExpr:
		p.print(token.MUL);
		optSemi = p.expr(x.X);

	case *ast.UnaryExpr:
		const prec = token.UnaryPrec;
		if prec < prec1 {
			// parenthesis needed
			p.print(token.LPAREN);
			p.expr(x);
			p.print(token.RPAREN);
		} else {
			// no parenthesis needed
			p.print(x.Op);
			if x.Op == token.RANGE {
				p.print(blank);
			}
			p.expr1(x.X, prec);
		}

	case *ast.BasicLit:
		p.print(x.Value);

	case *ast.StringList:
		p.stringList(x.Strings);

	case *ast.FuncLit:
		p.expr(x.Type);
		p.print(blank);
		p.level++;  // adjust nesting level for function body
		p.stmt(x.Body);
		p.level--;

	case *ast.ParenExpr:
		p.print(token.LPAREN);
		p.expr(x.X);
		p.print(x.Rparen, token.RPAREN);

	case *ast.SelectorExpr:
		p.expr1(x.X, token.HighestPrec);
		p.print(token.PERIOD);
		p.expr1(x.Sel, token.HighestPrec);

	case *ast.TypeAssertExpr:
		p.expr1(x.X, token.HighestPrec);
		p.print(token.PERIOD, token.LPAREN);
		if x.Type != nil {
			p.expr(x.Type);
		} else {
			p.print(token.TYPE);
		}
		p.print(token.RPAREN);

	case *ast.IndexExpr:
		p.expr1(x.X, token.HighestPrec);
		p.print(token.LBRACK);
		p.expr1(x.Index, token.LowestPrec);
		if x.End != nil {
			if needsBlanks(x.Index) || needsBlanks(x.End) {
				// blanks around ":"
				p.print(blank, token.COLON, blank);
			} else {
				// no blanks around ":"
				p.print(token.COLON);
			}
			p.expr(x.End);
		}
		p.print(token.RBRACK);

	case *ast.CallExpr:
		p.expr1(x.Fun, token.HighestPrec);
		p.print(x.Lparen, token.LPAREN);
		p.exprList(x.Args, commaSep);
		p.print(x.Rparen, token.RPAREN);

	case *ast.CompositeLit:
		p.expr1(x.Type, token.HighestPrec);
		p.print(x.Lbrace, token.LBRACE);
		p.exprList(x.Elts, commaSep | commaTerm);
		p.print(x.Rbrace, token.RBRACE);

	case *ast.Ellipsis:
		p.print(token.ELLIPSIS);

	case *ast.ArrayType:
		p.print(token.LBRACK);
		if x.Len != nil {
			p.expr(x.Len);
		}
		p.print(token.RBRACK);
		optSemi = p.expr(x.Elt);

	case *ast.StructType:
		p.print(token.STRUCT);
		p.fieldList(x.Lbrace, x.Fields, x.Rbrace, x.Incomplete, true);
		optSemi = true;

	case *ast.FuncType:
		p.print(token.FUNC);
		optSemi = p.signature(x.Params, x.Results);

	case *ast.InterfaceType:
		p.print(token.INTERFACE);
		p.fieldList(x.Lbrace, x.Methods, x.Rbrace, x.Incomplete, false);
		optSemi = true;

	case *ast.MapType:
		p.print(token.MAP, token.LBRACK);
		p.expr(x.Key);
		p.print(token.RBRACK);
		optSemi = p.expr(x.Value);

	case *ast.ChanType:
		switch x.Dir {
		case ast.SEND | ast.RECV:
			p.print(token.CHAN);
		case ast.RECV:
			p.print(token.ARROW, token.CHAN);
		case ast.SEND:
			p.print(token.CHAN, token.ARROW);
		}
		p.print(blank);
		optSemi = p.expr(x.Value);

	default:
		panic("unreachable");
	}

	return;
}


// Returns true if a separating semicolon is optional.
func (p *printer) expr(x ast.Expr) (optSemi bool) {
	return p.expr1(x, token.LowestPrec);
}


// ----------------------------------------------------------------------------
// Statements

// Print the statement list indented, but without a newline after the last statement.
func (p *printer) stmtList(list []ast.Stmt) {
	if len(list) > 0 {
		p.print(+1, formfeed);  // the next lines have different structure
		optSemi := false;
		for i, s := range list {
			if i > 0 {
				if !optSemi {
					p.print(token.SEMICOLON);
				}
				p.print(newline);
			}
			optSemi = p.stmt(s);
		}
		if !optSemi {
			p.print(token.SEMICOLON);
		}
		p.print(-1);
	}
}


func (p *printer) block(s *ast.BlockStmt) {
	p.print(s.Pos(), token.LBRACE);
	if len(s.List) > 0 {
		p.stmtList(s.List);
		p.print(formfeed);
	}
	p.print(s.Rbrace, token.RBRACE);
}


func (p *printer) switchBlock(s *ast.BlockStmt) {
	p.print(s.Pos(), token.LBRACE);
	if len(s.List) > 0 {
		for _, s := range s.List {
			// s is one of *ast.CaseClause, *ast.TypeCaseClause, *ast.CommClause;
			p.print(formfeed);
			p.stmt(s);
		}
		p.print(formfeed);
	}
	p.print(s.Rbrace, token.RBRACE);
}


func (p *printer) controlClause(isForStmt bool, init ast.Stmt, expr ast.Expr, post ast.Stmt) {
	if init == nil && post == nil {
		// no semicolons required
		if expr != nil {
			p.print(blank);
			p.expr(expr);
		}
	} else {
		// all semicolons required
		// (they are not separators, print them explicitly)
		p.print(blank);
		if init != nil {
			p.stmt(init);
		}
		p.print(token.SEMICOLON, blank);
		if expr != nil {
			p.expr(expr);
		}
		if isForStmt {
			p.print(token.SEMICOLON, blank);
			if post != nil {
				p.stmt(post);
			}
		}
	}
}


// Returns true if a separating semicolon is optional.
func (p *printer) stmt(stmt ast.Stmt) (optSemi bool) {
	p.print(stmt.Pos());

	switch s := stmt.(type) {
	case *ast.BadStmt:
		p.print("BadStmt");

	case *ast.DeclStmt:
		var comment *ast.CommentGroup;
		comment, optSemi = p.decl(s.Decl);
		if comment != nil {
			// Line comments of declarations in statement lists
			// are not associated with the declaration in the parser;
			// this case should never happen. Print anyway to continue
			// gracefully.
			p.lineComment(comment);
			p.print(newline);
		}

	case *ast.EmptyStmt:
		// nothing to do

	case *ast.LabeledStmt:
		p.print(-1, formfeed);
		p.expr(s.Label);
		p.print(token.COLON, tab, +1, formfeed);
		optSemi = p.stmt(s.Stmt);

	case *ast.ExprStmt:
		p.expr(s.X);

	case *ast.IncDecStmt:
		p.expr(s.X);
		p.print(s.Tok);

	case *ast.AssignStmt:
		p.exprList(s.Lhs, commaSep);
		p.print(blank, s.TokPos, s.Tok);
		p.exprList(s.Rhs, blankStart | commaSep);

	case *ast.GoStmt:
		p.print(token.GO, blank);
		p.expr(s.Call);

	case *ast.DeferStmt:
		p.print(token.DEFER, blank);
		p.expr(s.Call);

	case *ast.ReturnStmt:
		p.print(token.RETURN);
		if s.Results != nil {
			p.exprList(s.Results, blankStart | commaSep);
		}

	case *ast.BranchStmt:
		p.print(s.Tok);
		if s.Label != nil {
			p.print(blank);
			p.expr(s.Label);
		}

	case *ast.BlockStmt:
		p.block(s);
		optSemi = true;

	case *ast.IfStmt:
		p.print(token.IF);
		p.controlClause(false, s.Init, s.Cond, nil);
		p.print(blank);
		p.block(s.Body);
		optSemi = true;
		if s.Else != nil {
			p.print(blank, token.ELSE, blank);
			optSemi = p.stmt(s.Else);
		}

	case *ast.CaseClause:
		if s.Values != nil {
			p.print(token.CASE);
			p.exprList(s.Values, blankStart | commaSep);
		} else {
			p.print(token.DEFAULT);
		}
		p.print(s.Colon, token.COLON);
		p.stmtList(s.Body);

	case *ast.SwitchStmt:
		p.print(token.SWITCH);
		p.controlClause(false, s.Init, s.Tag, nil);
		p.print(blank);
		p.switchBlock(s.Body);
		optSemi = true;

	case *ast.TypeCaseClause:
		if s.Types != nil {
			p.print(token.CASE);
			p.exprList(s.Types, blankStart | commaSep);
		} else {
			p.print(token.DEFAULT);
		}
		p.print(s.Colon, token.COLON);
		p.stmtList(s.Body);

	case *ast.TypeSwitchStmt:
		p.print(token.SWITCH);
		if s.Init != nil {
			p.print(blank);
			p.stmt(s.Init);
			p.print(token.SEMICOLON);
		}
		p.print(blank);
		p.stmt(s.Assign);
		p.print(blank);
		p.switchBlock(s.Body);
		optSemi = true;

	case *ast.CommClause:
		if s.Rhs != nil {
			p.print(token.CASE, blank);
			if s.Lhs != nil {
				p.expr(s.Lhs);
				p.print(blank, s.Tok, blank);
			}
			p.expr(s.Rhs);
		} else {
			p.print(token.DEFAULT);
		}
		p.print(s.Colon, token.COLON);
		p.stmtList(s.Body);

	case *ast.SelectStmt:
		p.print(token.SELECT, blank);
		p.switchBlock(s.Body);
		optSemi = true;

	case *ast.ForStmt:
		p.print(token.FOR);
		p.controlClause(true, s.Init, s.Cond, s.Post);
		p.print(blank);
		p.block(s.Body);
		optSemi = true;

	case *ast.RangeStmt:
		p.print(token.FOR, blank);
		p.expr(s.Key);
		if s.Value != nil {
			p.print(token.COMMA, blank);
			p.expr(s.Value);
		}
		p.print(blank, s.TokPos, s.Tok, blank, token.RANGE, blank);
		p.expr(s.X);
		p.print(blank);
		p.block(s.Body);
		optSemi = true;

	default:
		panic("unreachable");
	}

	return;
}


// ----------------------------------------------------------------------------
// Declarations

// Returns line comment, if any, and whether a separating semicolon is optional.
// The parameters m and n control layout; m has different meanings for different
// specs, n is the number of specs in the group.
//
// ImportSpec:
//   m = number of imports with a rename
//
// ValueSpec:
//   m = number of values with a type
//
func (p *printer) spec(spec ast.Spec, m, n int) (comment *ast.CommentGroup, optSemi bool) {
	sep := separator(n > 1);

	switch s := spec.(type) {
	case *ast.ImportSpec:
		p.leadComment(s.Doc);
		if m > 0 {
			// at least one entry with a rename
			if s.Name != nil {
				p.expr(s.Name);
			}
			p.print(sep);
		}
		p.expr(&ast.StringList{s.Path});
		comment = s.Comment;

	case *ast.ValueSpec:
		p.leadComment(s.Doc);
		p.identList(s.Names);
		if m > 0 {
			// at least one entry with a type
			if s.Type != nil {
				p.print(sep);
				optSemi = p.expr(s.Type);
			} else if s.Values != nil {
				p.print(sep);
			}
		}
		if s.Values != nil {
			p.print(sep, token.ASSIGN);
			p.exprList(s.Values, blankStart | commaSep);
			optSemi = false;
		}
		comment = s.Comment;

	case *ast.TypeSpec:
		p.leadComment(s.Doc);
		p.expr(s.Name);
		p.print(sep);
		optSemi = p.expr(s.Type);
		comment = s.Comment;

	default:
		panic("unreachable");
	}

	return comment, optSemi;
}


func countImportRenames(list []ast.Spec) (n int) {
	for _, s := range list {
		if s.(*ast.ImportSpec).Name != nil {
			n++;
		}
	}
	return;
}


func countValueTypes(list []ast.Spec) (n int) {
	for _, s := range list {
		if s.(*ast.ValueSpec).Type != nil {
			n++;
		}
	}
	return;
}


// Returns true if a separating semicolon is optional.
func (p *printer) decl(decl ast.Decl) (comment *ast.CommentGroup, optSemi bool) {
	switch d := decl.(type) {
	case *ast.BadDecl:
		p.print(d.Pos(), "BadDecl");

	case *ast.GenDecl:
		p.leadComment(d.Doc);
		p.print(lineTag(d.Pos()), d.Tok, blank);

		// determine layout constant m
		var m int;
		switch d.Tok {
		case token.IMPORT:
			m = countImportRenames(d.Specs);
		case token.CONST, token.VAR:
			m = countValueTypes(d.Specs);
		}

		if d.Lparen.IsValid() {
			// group of parenthesized declarations
			p.print(d.Lparen, token.LPAREN);
			if len(d.Specs) > 0 {
				p.print(+1, formfeed);
				for i, s := range d.Specs {
					if i > 0 {
						p.print(token.SEMICOLON);
						p.lineComment(comment);
						p.print(newline);
					}
					comment, _ = p.spec(s, m, len(d.Specs));
				}
				p.print(token.SEMICOLON);
				p.lineComment(comment);
				p.print(-1, formfeed);
			}
			p.print(d.Rparen, token.RPAREN);
			comment = nil;  // comment was already printed
			optSemi = true;

		} else {
			// single declaration
			comment, optSemi = p.spec(d.Specs[0], m, 1);
		}

	case *ast.FuncDecl:
		p.leadComment(d.Doc);
		p.print(lineTag(d.Pos()), token.FUNC, blank);
		if recv := d.Recv; recv != nil {
			// method: print receiver
			p.print(token.LPAREN);
			if len(recv.Names) > 0 {
				p.expr(recv.Names[0]);
				p.print(blank);
			}
			p.expr(recv.Type);
			p.print(token.RPAREN, blank);
		}
		p.expr(d.Name);
		p.signature(d.Type.Params, d.Type.Results);
		if d.Body != nil {
			p.print(blank);
			p.level++;  // adjust nesting level for function body
			p.stmt(d.Body);
			p.level--;
		}

	default:
		panic("unreachable");
	}

	return comment, optSemi;
}


// ----------------------------------------------------------------------------
// Files

func (p *printer) file(src *ast.File) {
	p.leadComment(src.Doc);
	p.print(src.Pos(), token.PACKAGE, blank);
	p.expr(src.Name);

	for _, d := range src.Decls {
		p.print(newline, newline);
		comment, _ := p.decl(d);
		p.lineComment(comment);
	}

	p.print(newline);
}


// ----------------------------------------------------------------------------
// Public interface

var inf = token.Position{Offset: 1<<30, Line: 1<<30}


// Fprint "pretty-prints" an AST node to output and returns the number of
// bytes written, and an error, if any. The node type must be *ast.File,
// or assignment-compatible to ast.Expr, ast.Decl, or ast.Stmt. Printing
// is controlled by the mode and tabwidth parameters.
//
func Fprint(output io.Writer, node interface{}, mode uint, tabwidth int) (int, os.Error) {
	// setup tabwriter if needed and redirect output
	var tw *tabwriter.Writer;
	if mode & RawFormat == 0 {
		padchar := byte('\t');
		if mode & UseSpaces != 0 {
			padchar = ' ';
		}
		var twmode uint;
		if mode & GenHTML != 0 {
			twmode = tabwriter.FilterHTML;
		}
		tw = tabwriter.NewWriter(output, tabwidth, 1, padchar, twmode);
		output = tw;
	}

	// setup printer and print node
	var p printer;
	p.init(output, mode);
	go func() {
		switch n := node.(type) {
		case ast.Expr:
			p.expr(n);
		case ast.Stmt:
			p.stmt(n);
		case ast.Decl:
			comment, _ := p.decl(n);
			p.lineComment(comment);  // no newline at end
		case *ast.File:
			p.comment = n.Comments;
			p.file(n);
		default:
			p.errors <- os.NewError("unsupported node type");
		}
		p.flush(inf);
		p.errors <- nil;  // no errors
	}();
	err := <-p.errors;  // wait for completion of goroutine

	// flush tabwriter, if any
	if tw != nil {
		tw.Flush();  // ignore errors
	}

	return p.written, err;
}
