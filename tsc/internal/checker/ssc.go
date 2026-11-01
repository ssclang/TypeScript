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
		if declaration == nil || !c.ssc_isPreludeDeclaration(declaration) {
			continue
		}
		brand := ssc_getSscBrandTypeLiteral(declaration, name)
		if brand == nil {
			continue
		}
		symbolByBrand[brand.Symbol()] = symbol
		symbolByName[name] = symbol
	}
	c.ssc_PreludeSymbolByBrand = symbolByBrand
	c.ssc_PreludeSymbolByName = symbolByName
	c.ssc_Enabled = len(symbolByName) == len(ssc_types)
	return symbolByBrand, symbolByName
}

func (c *Checker) ssc_isPreludeDeclaration(declaration *ast.Node) bool {
	sourceFile := ast.GetSourceFileOfNode(declaration)
	if sourceFile == nil || sourceFile.FileName().BaseName() != "prelude.d.ts" {
		return false
	}
	packageJsonDirectory := c.program.GetSourceFileMetaData(sourceFile.PathKey()).PackageJsonDirectory
	if packageJsonDirectory == "" {
		return false
	}
	packageJson := c.program.GetPackageJsonInfo(packageJsonDirectory.ResolveFile("package.json")).GetContents()
	if packageJson == nil {
		return false
	}
	packageName, ok := packageJson.Name.GetValue()
	return ok && packageName == "syscript"
}

func (c *Checker) ssc_bstringType() *Type {
	symbol := c.getGlobalSymbol("bstring", ast.SymbolFlagsInterface, nil)
	if symbol == nil {
		return nil
	}
	declaration := ast.GetDeclarationOfKind(symbol, ast.KindInterfaceDeclaration)
	if declaration == nil || !c.ssc_isPreludeDeclaration(declaration) {
		return nil
	}
	return c.getDeclaredTypeOfSymbol(symbol)
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

func (c *Checker) ssc_type(t *Type) *Type {
	symbolByBrand, _ := c.ssc_loadPrelude()
	if t.flags&TypeFlagsNumberLike != 0 {
		return c.numberType
	}
	if t.flags&TypeFlagsIntersection == 0 {
		return nil
	}
	for memberType := range slices.Values(t.Types()) {
		if symbol := symbolByBrand[memberType.symbol]; symbol != nil {
			return c.getDeclaredTypeOfSymbol(symbol)
		}
	}
	if slices.ContainsFunc(t.Types(), func(memberType *Type) bool { return memberType.flags&TypeFlagsNumberLike != 0 }) {
		return c.numberType
	}
	return nil
}

func (c *Checker) ssc_name(t *Type) string {
	if t == c.numberType {
		return "number"
	}
	_, symbolByName := c.ssc_loadPrelude()
	for name, symbol := range symbolByName {
		if t == c.getDeclaredTypeOfSymbol(symbol) {
			return name
		}
	}
	panic("not a syscript number type: " + c.TypeToString(t))
}

func (c *Checker) ssc_IsBrandToNumber(source *Type, target *Type) bool {
	if !c.ssc_IsEnabled() {
		return false
	}
	if sscType := c.ssc_type(source); sscType == nil || sscType == c.numberType {
		return false
	}
	return c.ssc_type(target) == c.numberType
}

func (c *Checker) ssc_IsNumberLikeCheck(source *Type, target *Type) bool {
	if target != c.numberType && target != c.numberOrBigIntType || !c.ssc_IsEnabled() {
		return false
	}
	isSsc, _ := c.ssc_CommonType(source)
	return isSsc
}

func (c *Checker) ssc_IsEnabled() bool {
	c.ssc_loadPrelude()
	return c.ssc_Enabled
}

func (c *Checker) ssc_CommonType(types ...*Type) (isSsc bool, commonType *Type) {
	if !c.ssc_IsEnabled() {
		return false, nil
	}
	var sscTypes []*Type
	for t := range slices.Values(types) {
		memberTypes := []*Type{t}
		if t.flags&TypeFlagsUnion != 0 {
			memberTypes = t.Types()
		}
		for memberType := range slices.Values(memberTypes) {
			sscType := c.ssc_type(memberType)
			if sscType == nil {
				return false, nil
			}
			sscTypes = append(sscTypes, sscType)
		}
	}
	if len(sscTypes) == 0 {
		return false, nil
	}
	if slices.ContainsFunc(sscTypes, func(sscType *Type) bool { return sscType != sscTypes[0] }) {
		return true, nil
	}
	return true, sscTypes[0]
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
		name := c.ssc_name(result)
		if operator == ast.KindMinusToken && slices.Contains(ssc_unsignedTypes, name) ||
			slices.Contains(ssc_floatTypes, name) && slices.Contains(ssc_bitwiseOperators, operator) {
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
		name := c.ssc_name(result)
		if slices.Contains(ssc_floatTypes, name) && slices.Contains(ssc_bitwiseOperators, operator) {
			result = nil
		}
	}
	if result == nil {
		c.reportOperatorError(left, operator, right, errorNode, nil)
		return c.errorType
	}
	return result
}

func (c *Checker) ssc_ConcatResultTypeOrReportError(left *Type, right *Type, operator ast.Kind, errorNode *ast.Node) *Type {
	bstringType := c.ssc_bstringType()
	if bstringType == nil || left != bstringType && right != bstringType {
		return nil
	}
	if left != right {
		c.reportOperatorError(left, operator, right, errorNode, nil)
		return c.errorType
	}
	return bstringType
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
