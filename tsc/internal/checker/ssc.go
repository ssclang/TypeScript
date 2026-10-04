package checker

import (
	"slices"

	"github.com/microsoft/TypeScript/tsc/internal/ast"
	"github.com/microsoft/TypeScript/tsc/internal/diagnostics"
	"github.com/microsoft/TypeScript/tsc/internal/scanner"
)

var ssc_sortedTypes = []string{"i8", "u8", "i16", "u16", "i32", "u32", "i64", "u64", "f32", "f64"}

var ssc_floatTypes = []string{"f32", "f64"}

var ssc_bitwiseOperators = []ast.Kind{
	ast.KindAmpersandEqualsToken,
	ast.KindAmpersandToken,
	ast.KindBarEqualsToken,
	ast.KindBarToken,
	ast.KindCaretEqualsToken,
	ast.KindCaretToken,
	ast.KindGreaterThanGreaterThanEqualsToken,
	ast.KindGreaterThanGreaterThanGreaterThanEqualsToken,
	ast.KindGreaterThanGreaterThanGreaterThanToken,
	ast.KindGreaterThanGreaterThanToken,
	ast.KindLessThanLessThanEqualsToken,
	ast.KindLessThanLessThanToken,
	ast.KindTildeToken,
}

var ssc_losslessTargets = map[string][]string{
	"i8":  {"i8", "i16", "i32", "i64", "f32", "f64"},
	"i16": {"i16", "i32", "i64", "f32", "f64"},
	"i32": {"i32", "i64", "f64"},
	"i64": {"i64"},
	"u8":  {"u8", "u16", "u32", "u64", "i16", "i32", "i64", "f32", "f64"},
	"u16": {"u16", "u32", "u64", "i32", "i64", "f32", "f64"},
	"u32": {"u32", "u64", "i64", "f64"},
	"u64": {"u64"},
	"f32": {"f32", "f64"},
	"f64": {"f64"},
}

func (c *Checker) ssc_loadPrelude() (symbolSet map[*ast.Symbol]bool, symbolByName map[string]*ast.Symbol) {
	if c.ssc_PreludeSymbolSet != nil {
		return c.ssc_PreludeSymbolSet, c.ssc_PreludeSymbolByName
	}
	symbolSet = map[*ast.Symbol]bool{}
	symbolByName = map[string]*ast.Symbol{}
	for name := range slices.Values(ssc_sortedTypes) {
		symbol := c.getGlobalSymbol(name, ast.SymbolFlagsTypeAlias, nil)
		if symbol == nil {
			continue
		}
		declaration := ast.GetDeclarationOfKind(symbol, ast.KindTypeAliasDeclaration)
		if declaration == nil {
			continue
		}
		sourceFile := ast.GetSourceFileOfNode(declaration)
		if sourceFile == nil || sourceFile.FileName().BaseName() != "prelude.d.ts" {
			continue
		}
		packageJsonDirectory := c.program.GetSourceFileMetaData(sourceFile.PathKey()).PackageJsonDirectory
		if packageJsonDirectory == "" {
			continue
		}
		packageJson := c.program.GetPackageJsonInfo(packageJsonDirectory.ResolveFile("package.json")).GetContents()
		if packageJson == nil {
			continue
		}
		if packageName, ok := packageJson.Name.GetValue(); ok && packageName == "syscript" {
			symbolSet[symbol] = true
			symbolByName[name] = symbol
		}
	}
	c.ssc_PreludeSymbolSet = symbolSet
	c.ssc_PreludeSymbolByName = symbolByName
	return symbolSet, symbolByName
}

func (c *Checker) ssc_symbol(t *Type) *ast.Symbol {
	symbolSet, symbolByName := c.ssc_loadPrelude() // loaded on the first lookup instead of in NewChecker, to keep code injected into checker.go minimal
	if t.flags&TypeFlagsNumber != 0 {
		return symbolByName["f64"]
	}
	if symbol := t.alias.Symbol(); symbolSet[symbol] {
		return symbol
	}
	return nil
}

func (c *Checker) ssc_CommonType(types ...*Type) (isSsc bool, commonType *Type) {
	var symbols []*ast.Symbol
	for t := range slices.Values(types) {
		memberTypes := []*Type{t}
		if t.flags&TypeFlagsUnion != 0 {
			memberTypes = t.Types()
		}
		for memberType := range slices.Values(memberTypes) {
			if memberType.flags&TypeFlagsNumberLiteral != 0 {
				continue
			}
			symbol := c.ssc_symbol(memberType)
			if symbol == nil {
				return false, nil
			}
			symbols = append(symbols, symbol)
		}
	}
	if len(symbols) == 0 {
		return false, nil
	}
	for name := range slices.Values(ssc_sortedTypes) {
		if slices.ContainsFunc(symbols, func(symbol *ast.Symbol) bool { return !slices.Contains(ssc_losslessTargets[symbol.Name()], name) }) {
			continue
		}
		_, symbolByName := c.ssc_loadPrelude()
		symbol := symbolByName[name]
		if symbol == nil {
			return true, nil
		}
		return true, c.getDeclaredTypeOfSymbol(symbol)
	}
	return true, nil
}

func (c *Checker) ssc_UnaryResultTypeOrReportError(operandType *Type, operator ast.Kind, errorNode *ast.Node) *Type {
	isSsc, result := c.ssc_CommonType(operandType)
	if !isSsc {
		return nil
	}
	if operator == ast.KindPlusToken {
		result = nil
		_, symbolByName := c.ssc_loadPrelude()
		if f64Symbol := symbolByName["f64"]; f64Symbol != nil {
			_, result = c.ssc_CommonType(operandType, c.getDeclaredTypeOfSymbol(f64Symbol))
		}
	}
	if result != nil && slices.Contains(ssc_floatTypes, c.ssc_symbol(result).Name()) && slices.Contains(ssc_bitwiseOperators, operator) {
		result = nil
	}
	if result == nil {
		c.error(errorNode, diagnostics.Operator_0_cannot_be_applied_to_type_1, scanner.TokenToString(operator), c.TypeToString(operandType))
		return c.errorType
	}
	return result
}

func (c *Checker) ssc_BinaryResultType(left *Type, right *Type) (isSsc bool, result *Type) {
	leftIsSsc, _ := c.ssc_CommonType(left)
	rightIsSsc, _ := c.ssc_CommonType(right)
	if !leftIsSsc && !rightIsSsc {
		return false, nil
	}
	_, result = c.ssc_CommonType(left, right)
	return true, result
}

func (c *Checker) ssc_BinaryResultTypeOrReportError(left *Type, right *Type, operator ast.Kind, errorNode *ast.Node) *Type {
	isSsc, result := c.ssc_BinaryResultType(left, right)
	if !isSsc {
		return nil
	}
	if result != nil && slices.Contains(ssc_floatTypes, c.ssc_symbol(result).Name()) && slices.Contains(ssc_bitwiseOperators, operator) {
		result = nil
	}
	if result == nil {
		c.reportOperatorError(left, operator, right, errorNode, nil)
		return c.errorType
	}
	return result
}

func (c *Checker) ssc_Related(source *Type, target *Type, relation *Relation) (isSsc bool, related bool) {
	sourceSymbol := c.ssc_symbol(source)
	targetSymbol := c.ssc_symbol(target)
	if sourceSymbol == nil || targetSymbol == nil {
		return false, false
	}
	if relation == c.identityRelation || relation == c.subtypeRelation || relation == c.strictSubtypeRelation {
		return true, sourceSymbol == targetSymbol
	}
	if relation == c.comparableRelation {
		_, commonType := c.ssc_CommonType(source, target)
		return true, commonType != nil
	}
	if relation == c.assignableRelation {
		return true, slices.Contains(ssc_losslessTargets[sourceSymbol.Name()], targetSymbol.Name())
	}
	return false, false
}

func (r *Relater) ssc_RelatedOrReportError(originalSource *Type, originalTarget *Type, source *Type, target *Type, reportErrors bool, headMessage *diagnostics.Message) (isSsc bool, result Ternary) {
	isSsc, related := r.c.ssc_Related(source, target, r.relation)
	if !isSsc {
		return false, TernaryFalse
	}
	if !related {
		if reportErrors {
			r.reportErrorResults(originalSource, originalTarget, source, target, headMessage)
		}
		return true, TernaryFalse
	}
	return true, TernaryTrue
}
