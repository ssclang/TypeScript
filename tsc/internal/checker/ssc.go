package checker

import (
	"fmt"
	"slices"
	"strings"

	"github.com/microsoft/TypeScript/tsc/internal/ast"
	"github.com/microsoft/TypeScript/tsc/internal/diagnostics"
	"github.com/microsoft/TypeScript/tsc/internal/scanner"
)

var ssc_signedTypes = []string{"i8", "i16", "i32", "i64"}

var ssc_unsignedTypes = []string{"u8", "u16", "u32", "u64", "char"}

var ssc_floatTypes = []string{"f32", "f64"}

var ssc_types = slices.Concat(ssc_signedTypes, ssc_unsignedTypes, ssc_floatTypes)

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

func (c *Checker) ssc_loadPrelude() (symbolByBrand map[*ast.Symbol]*ast.Symbol, symbolByName map[string]*ast.Symbol) {
	if c.ssc_PreludeSymbolByBrand != nil {
		return c.ssc_PreludeSymbolByBrand, c.ssc_PreludeSymbolByName
	}
	symbolByBrand = map[*ast.Symbol]*ast.Symbol{}
	symbolByName = map[string]*ast.Symbol{}
	for name := range slices.Values(ssc_types) {
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
			if name == "f64" {
				symbolByName[name] = symbol
				continue
			}
			brand := ssc_getSscBrandTypeLiteral(declaration, name)
			if brand == nil {
				continue
			}
			symbolByBrand[brand.Symbol()] = symbol
			symbolByName[name] = symbol
		}
	}
	c.ssc_PreludeSymbolByBrand = symbolByBrand
	c.ssc_PreludeSymbolByName = symbolByName
	c.ssc_Enabled = len(symbolByName) == len(ssc_types)
	return symbolByBrand, symbolByName
}

func ssc_getSscBrandTypeLiteral(declaration *ast.Node, expectedName string) *ast.Node {
	typeNode := declaration.Type()
	if !ast.IsIntersectionTypeNode(typeNode) {
		return nil
	}
	expectedProperty := fmt.Sprintf("readonly [ssc]: '%s'", expectedName)
	for memberTypeNode := range slices.Values(typeNode.AsIntersectionTypeNode().Types.Nodes) {
		if ast.IsTypeLiteralNode(memberTypeNode) && strings.Contains(scanner.GetTextOfNode(memberTypeNode), expectedProperty) {
			return memberTypeNode
		}
	}
	return nil
}

func (c *Checker) ssc_symbol(t *Type) *ast.Symbol {
	symbolByBrand, symbolByName := c.ssc_loadPrelude()
	if t.flags&(TypeFlagsNumber|TypeFlagsNumberLiteral) != 0 {
		return symbolByName["f64"]
	}
	if t.flags&TypeFlagsIntersection == 0 {
		return nil
	}
	if f64Symbol := symbolByName["f64"]; f64Symbol != nil && t == c.getDeclaredTypeOfSymbol(f64Symbol) {
		return f64Symbol
	}
	for memberType := range slices.Values(t.Types()) {
		if symbol := symbolByBrand[memberType.symbol]; symbol != nil {
			return symbol
		}
	}
	return nil
}

func (c *Checker) ssc_IsEnabled() bool {
	c.ssc_loadPrelude()
	return c.ssc_Enabled
}

func (c *Checker) ssc_CommonType(types ...*Type) (isSsc bool, commonType *Type) {
	if !c.ssc_IsEnabled() {
		return false, nil
	}
	var symbols []*ast.Symbol
	for t := range slices.Values(types) {
		memberTypes := []*Type{t}
		if t.flags&TypeFlagsUnion != 0 {
			memberTypes = t.Types()
		}
		for memberType := range slices.Values(memberTypes) {
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
	if slices.ContainsFunc(symbols, func(symbol *ast.Symbol) bool { return symbol != symbols[0] }) {
		return true, nil
	}
	return true, c.getDeclaredTypeOfSymbol(symbols[0])
}

func (c *Checker) ssc_UnaryResultTypeOrReportError(operandType *Type, operator ast.Kind, errorNode *ast.Node) *Type {
	if !c.ssc_IsEnabled() {
		return nil
	}
	isSsc, result := c.ssc_CommonType(operandType)
	if !isSsc {
		return nil
	}
	if result != nil {
		name := c.ssc_symbol(result).Name()
		if operator == ast.KindMinusToken && slices.Contains(ssc_unsignedTypes, name) ||
			name == "f32" && slices.Contains(ssc_bitwiseOperators, operator) {
			result = nil
		}
	}
	if result == nil {
		c.error(errorNode, diagnostics.Operator_0_cannot_be_applied_to_type_1, scanner.TokenToString(operator), c.TypeToString(operandType))
		return c.errorType
	}
	return result
}

func (c *Checker) ssc_BinaryResultType(left *Type, right *Type) (isSsc bool, result *Type) {
	if !c.ssc_IsEnabled() {
		return false, nil
	}
	leftIsSsc, _ := c.ssc_CommonType(left)
	rightIsSsc, _ := c.ssc_CommonType(right)
	if !leftIsSsc && !rightIsSsc {
		return false, nil
	}
	_, result = c.ssc_CommonType(left, right)
	return true, result
}

func (c *Checker) ssc_BinaryResultTypeOrReportError(left *Type, right *Type, operator ast.Kind, errorNode *ast.Node) *Type {
	if !c.ssc_IsEnabled() {
		return nil
	}
	isSsc, result := c.ssc_BinaryResultType(left, right)
	if !isSsc {
		return nil
	}
	if result != nil {
		name := c.ssc_symbol(result).Name()
		if name == "f32" && slices.Contains(ssc_bitwiseOperators, operator) {
			result = nil
		}
	}
	if result == nil {
		c.reportOperatorError(left, operator, right, errorNode, nil)
		return c.errorType
	}
	return result
}

func (c *Checker) ssc_LogicalResultTypeOrReportError(left *Type, right *Type, operator ast.Kind, errorNode *ast.Node) *Type {
	if !c.ssc_IsEnabled() {
		return nil
	}
	if leftIsSsc, _ := c.ssc_CommonType(left); !leftIsSsc {
		return nil
	}
	if rightIsSsc, _ := c.ssc_CommonType(right); !rightIsSsc {
		return nil
	}
	_, result := c.ssc_CommonType(left, right)
	if result == nil {
		c.reportOperatorError(left, operator, right, errorNode, nil)
		return c.errorType
	}
	return result
}
