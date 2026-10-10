package checker

import (
	"cmp"
	"fmt"
	"math"
	"math/big"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/microsoft/TypeScript/tsc/internal/ast"
	"github.com/microsoft/TypeScript/tsc/internal/diagnostics"
	"github.com/microsoft/TypeScript/tsc/internal/jsnum"
	"github.com/microsoft/TypeScript/tsc/internal/scanner"
)

var ssc_signedTypes = []string{"i8", "i16", "i32", "i64"}

var ssc_unsignedTypes = []string{"u8", "u16", "u32", "u64"}

var ssc_floatTypes = []string{"f32", "f64"}

var ssc_types = slices.Concat(ssc_signedTypes, ssc_unsignedTypes, ssc_floatTypes, []string{"char"})

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
	"i8":   {"i8", "i16", "i32", "i64", "f32", "f64"},
	"i16":  {"i16", "i32", "i64", "f32", "f64"},
	"i32":  {"i32", "i64", "f64"},
	"i64":  {"i64"},
	"u8":   {"u8", "u16", "u32", "u64", "i16", "i32", "i64", "f32", "f64"},
	"u16":  {"u16", "u32", "u64", "i32", "i64", "f32", "f64"},
	"u32":  {"u32", "u64", "i64", "f64"},
	"u64":  {"u64"},
	"f32":  {"f32", "f64"},
	"f64":  {"f64"},
	"char": {"char", "u32", "u64", "i64", "f64"},
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
	if t.flags&TypeFlagsNumber != 0 {
		return symbolByName["f64"]
	}
	if t.flags&TypeFlagsIntersection == 0 {
		return nil
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
	var literals []*Type
	hasCharLiteral := false
	for t := range slices.Values(types) {
		memberTypes := []*Type{t}
		if t.flags&TypeFlagsUnion != 0 {
			memberTypes = t.Types()
		}
		for memberType := range slices.Values(memberTypes) {
			if memberType.flags&TypeFlagsNumberLiteral != 0 {
				literals = append(literals, memberType)
				continue
			}
			if c.ssc_isCharLiteral(memberType) {
				hasCharLiteral = true
				continue
			}
			symbol := c.ssc_symbol(memberType)
			if symbol == nil {
				return false, nil
			}
			symbols = append(symbols, symbol)
		}
	}
	if len(symbols) == 0 || hasCharLiteral && !slices.ContainsFunc(symbols, func(symbol *ast.Symbol) bool { return symbol.Name() == "char" }) {
		return false, nil
	}
	isFloat := slices.ContainsFunc(symbols, func(symbol *ast.Symbol) bool { return slices.Contains(ssc_floatTypes, symbol.Name()) }) ||
		slices.ContainsFunc(literals, func(literal *Type) bool {
			isInteger, _ := c.ssc_literalInteger(literal)
			return !isInteger
		})
	isSigned := slices.ContainsFunc(symbols, func(symbol *ast.Symbol) bool { return slices.Contains(ssc_signedTypes, symbol.Name()) }) ||
		slices.ContainsFunc(literals, func(literal *Type) bool {
			isInteger, value := c.ssc_literalInteger(literal)
			return isInteger && value.Sign() < 0
		})
	var candidates []string
	if isFloat {
		candidates = ssc_floatTypes
	} else if isSigned {
		candidates = slices.Concat(ssc_signedTypes, ssc_floatTypes)
	} else {
		candidates = slices.Concat(ssc_unsignedTypes, ssc_floatTypes)
	}
	for name := range slices.Values(candidates) {
		if slices.ContainsFunc(symbols, func(symbol *ast.Symbol) bool { return !slices.Contains(ssc_losslessTargets[symbol.Name()], name) }) {
			continue
		}
		if slices.ContainsFunc(literals, func(literal *Type) bool { return !c.ssc_isLiteralInRange(literal, name) }) {
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
	if !c.ssc_IsEnabled() {
		return nil
	}
	isSsc, result := c.ssc_CommonType(operandType)
	if !isSsc {
		return nil
	}
	_, symbolByName := c.ssc_loadPrelude()
	if (operator == ast.KindPlusPlusToken || operator == ast.KindMinusMinusToken) && c.ssc_symbol(operandType) == symbolByName["char"] {
		result = nil
	} else if operator == ast.KindPlusToken {
		result = nil
		if f64Symbol := symbolByName["f64"]; f64Symbol != nil {
			_, result = c.ssc_CommonType(operandType, c.getDeclaredTypeOfSymbol(f64Symbol))
		}
	} else if operator == ast.KindMinusToken {
		_, result = c.ssc_CommonType(operandType, c.getNumberLiteralType(-1))
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
	if !c.ssc_IsEnabled() {
		return false, false
	}
	sourceSymbol := c.ssc_symbol(source)
	targetSymbol := c.ssc_symbol(target)
	if (relation == c.assignableRelation || relation == c.subtypeRelation || relation == c.strictSubtypeRelation) && targetSymbol != nil && source.flags&(TypeFlagsNumberLiteral|TypeFlagsStringLiteral) != 0 {
		return true, c.ssc_isLiteralInRange(source, targetSymbol.Name())
	}
	if relation == c.comparableRelation && (sourceSymbol != nil || targetSymbol != nil) {
		if isSsc, commonType := c.ssc_CommonType(source, target); isSsc {
			return true, commonType != nil
		}
		return false, false
	}
	if sourceSymbol == nil || targetSymbol == nil {
		return false, false
	}
	if relation == c.identityRelation || relation == c.subtypeRelation || relation == c.strictSubtypeRelation {
		return true, sourceSymbol == targetSymbol
	}
	if relation == c.assignableRelation {
		return true, slices.Contains(ssc_losslessTargets[sourceSymbol.Name()], targetSymbol.Name())
	}
	return false, false
}

func (r *Relater) ssc_RelatedOrReportError(originalSource *Type, originalTarget *Type, source *Type, target *Type, reportErrors bool, headMessage *diagnostics.Message) (isSsc bool, result Ternary) {
	if !r.c.ssc_IsEnabled() {
		return false, TernaryFalse
	}
	isSsc, related := r.c.ssc_Related(source, target, r.relation)
	if !isSsc {
		return false, TernaryFalse
	}
	if related {
		return true, TernaryTrue
	}
	if reportErrors {
		if source.flags&(TypeFlagsNumberLiteral|TypeFlagsStringLiteral) != 0 && r.relation == r.c.assignableRelation {
			message := cmp.Or(headMessage, diagnostics.Type_0_is_not_assignable_to_type_1)
			sourceType, targetType := r.c.getTypeNamesForErrorDisplay(originalSource, originalTarget)
			r.reportError(message, sourceType, targetType)
		} else if source.flags&(TypeFlagsNumberLiteral|TypeFlagsStringLiteral) != 0 && r.relation == r.c.comparableRelation {
			message := cmp.Or(headMessage, diagnostics.Type_0_is_not_comparable_to_type_1)
			sourceType, targetType := r.c.getTypeNamesForErrorDisplay(originalSource, originalTarget)
			r.reportError(message, sourceType, targetType)
		} else {
			r.reportErrorResults(originalSource, originalTarget, source, target, headMessage)
		}
	}
	return true, TernaryFalse
}

func (c *Checker) ssc_IsLiteralOfContextualType(candidateType *Type, contextualType *Type) (isSsc bool, result bool) {
	if !c.ssc_IsEnabled() || contextualType.flags&TypeFlagsNumber != 0 || c.ssc_symbol(contextualType) == nil {
		return false, false
	}
	return true, c.maybeTypeOfKind(candidateType, TypeFlagsNumberLiteral|TypeFlagsStringLiteral)
}

func (c *Checker) ssc_isCharLiteral(t *Type) bool {
	if t.flags&TypeFlagsStringLiteral == 0 {
		return false
	}
	value := t.AsLiteralType().value.(string)
	return utf8.ValidString(value) && utf8.RuneCountInString(value) == 1
}

var ssc_integerRanges = map[string]struct{ min, max *big.Int }{
	"i8":  {big.NewInt(math.MinInt8), big.NewInt(math.MaxInt8)},
	"i16": {big.NewInt(math.MinInt16), big.NewInt(math.MaxInt16)},
	"i32": {big.NewInt(math.MinInt32), big.NewInt(math.MaxInt32)},
	"i64": {big.NewInt(math.MinInt64), big.NewInt(math.MaxInt64)},
	"u8":  {big.NewInt(0), big.NewInt(math.MaxUint8)},
	"u16": {big.NewInt(0), big.NewInt(math.MaxUint16)},
	"u32": {big.NewInt(0), big.NewInt(math.MaxUint32)},
	"u64": {big.NewInt(0), new(big.Int).SetUint64(math.MaxUint64)},
}

func (c *Checker) ssc_NumberLiteralType(node *ast.Node, negative bool) *Type {
	if !c.ssc_IsEnabled() {
		return nil
	}
	text := scanner.GetTextOfNode(node)
	if node.AsNumericLiteral().TokenFlags&(ast.TokenFlagsScientific|ast.TokenFlagsOctal) != 0 || strings.Contains(text, ".") {
		return nil
	}
	value, _ := new(big.Int).SetString(jsnum.ParsePseudoBigInt(strings.ReplaceAll(text, "_", "")), 10)
	if value.CmpAbs(ssc_integerRanges["u64"].max) > 0 {
		return nil
	}
	if negative {
		value.Neg(value)
	}
	float, accuracy := new(big.Float).SetInt(value).Float64()
	if accuracy == big.Exact {
		return nil
	}
	if c.ssc_LiteralTypeByValue == nil {
		c.ssc_LiteralTypeByValue = map[string]*Type{}
		c.ssc_LiteralValueByType = map[*Type]*big.Int{}
	}
	key := value.String()
	t := c.ssc_LiteralTypeByValue[key]
	if t == nil {
		t = c.newLiteralType(TypeFlagsNumberLiteral, jsnum.Number(float), nil)
		c.ssc_LiteralTypeByValue[key] = t
		c.ssc_LiteralValueByType[t] = value
	}
	return t
}

func (b *NodeBuilderImpl) ssc_NumberLiteralTypeNode(t *Type) *ast.TypeNode {
	if !b.ch.ssc_IsEnabled() {
		return nil
	}
	isInteger, value := b.ch.ssc_literalInteger(t)
	if !isInteger || value.CmpAbs(ssc_integerRanges["u64"].max) > 0 {
		return nil
	}
	text := value.String()
	b.ctx.approximateLength += len(text)
	if after, negative := strings.CutPrefix(text, "-"); negative {
		return b.f.NewLiteralTypeNode(b.f.NewPrefixUnaryExpression(ast.KindMinusToken, b.f.NewNumericLiteral(after, ast.TokenFlagsNone)))
	}
	return b.f.NewLiteralTypeNode(b.f.NewNumericLiteral(text, ast.TokenFlagsNone))
}

func (c *Checker) ssc_literalInteger(t *Type) (isInteger bool, value *big.Int) {
	if regularType := t.AsLiteralType().regularType; regularType != nil {
		t = regularType
	}
	if value := c.ssc_LiteralValueByType[t]; value != nil {
		return true, value
	}
	number := float64(t.AsLiteralType().value.(jsnum.Number))
	if math.IsNaN(number) || math.IsInf(number, 0) {
		return false, nil
	}
	float := big.NewFloat(number)
	if !float.IsInt() {
		return false, nil
	}
	value, _ = float.Int(nil)
	return true, value
}

func (c *Checker) ssc_isLiteralInRange(literal *Type, name string) bool {
	if literal.flags&TypeFlagsStringLiteral != 0 {
		return slices.Contains(ssc_losslessTargets["char"], name) && c.ssc_isCharLiteral(literal)
	}
	if slices.Contains(ssc_floatTypes, name) {
		return true
	}
	integer, ok := ssc_integerRanges[name]
	if !ok {
		return false
	}
	isInteger, value := c.ssc_literalInteger(literal)
	if !isInteger {
		return false
	}
	return integer.min.Cmp(value) <= 0 && value.Cmp(integer.max) <= 0
}

func (c *Checker) ssc_IsLiteralAgainstSscType(literal *Type, other *Type) bool {
	if !c.ssc_IsEnabled() || c.ssc_symbol(other) == nil {
		return false
	}
	return everyType(literal, func(t *Type) bool { return t.flags&TypeFlagsNumberLiteral != 0 }) || everyType(literal, func(t *Type) bool { return t.flags&TypeFlagsStringLiteral != 0 })
}
