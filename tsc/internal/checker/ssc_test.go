package checker_test

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"

	"github.com/microsoft/TypeScript/tsc/internal/ast"
	"github.com/microsoft/TypeScript/tsc/internal/bundled"
	"github.com/microsoft/TypeScript/tsc/internal/compiler"
	"github.com/microsoft/TypeScript/tsc/internal/core"
	"github.com/microsoft/TypeScript/tsc/internal/diagnosticwriter"
	"github.com/microsoft/TypeScript/tsc/internal/locale"
	"github.com/microsoft/TypeScript/tsc/internal/tsoptions"
	"github.com/microsoft/TypeScript/tsc/internal/tspath"
	"github.com/microsoft/TypeScript/tsc/internal/vfs/vfstest"
	"gotest.tools/v3/assert"
)

const ssc_prelude = `export {};
declare const ssc: unique symbol;
declare global {
  type i8 = number & { readonly [ssc]: 'i8' };
  type i16 = number & { readonly [ssc]: 'i16' };
  type i32 = number & { readonly [ssc]: 'i32' };
  type i64 = number & { readonly [ssc]: 'i64' };
  type u8 = number & { readonly [ssc]: 'u8' };
  type u16 = number & { readonly [ssc]: 'u16' };
  type u32 = number & { readonly [ssc]: 'u32' };
  type u64 = number & { readonly [ssc]: 'u64' };
  type f32 = number & { readonly [ssc]: 'f32' };
  type f64 = number & { readonly [ssc]: 'f64' };
  type char = number & { readonly [ssc]: 'char' };
  interface Array<T> { length: u64; [n: number]: T; [n: i64]: T; [n: u64]: T }
  interface Boolean {}
  interface CallableFunction {}
  interface Function {}
  interface IArguments {}
  interface NewableFunction {}
  interface Number {}
  interface Object {}
  interface RegExp {}
  interface String {}
}
`

const ssc_declarations = `declare const c: boolean;
declare const n: number;
declare const x: any;
declare const lit: 1 | 2;
declare const items: string[];
declare function abs(v: number): number;
enum E { A, B }
declare const e: E;
declare let a8: i8;
declare let a16: i16;
declare let a32: i32;
declare let b32: i32;
declare let a64: i64;
declare let ua8: u8;
declare let ua32: u32;
declare let ua64: u64;
declare let fa32: f32;
declare let fa64: f64;
declare let ch: char;
declare let ch2: char;
`

type ssc_result struct {
	resultType      string
	codes           []int32
	messages        []string
	suggestionCodes []int32
}

func ssc_messageTexts(diagnostic *ast.Diagnostic) []string {
	texts := []string{diagnosticwriter.WrapASTDiagnostic(diagnostic).Localize(locale.Default)}
	for chained := range slices.Values(diagnostic.MessageChain()) {
		texts = append(texts, ssc_messageTexts(chained)...)
	}
	return texts
}

func ssc_check(t *testing.T, files map[string]string, rootFiles []string) ssc_result {
	t.Helper()
	config, err := json.Marshal(map[string]any{
		"compilerOptions": map[string]any{"strict": true, "noEmit": true, "noLib": true},
		"files":           rootFiles,
	})
	assert.NilError(t, err)
	files["/tsconfig.json"] = string(config)
	fs := bundled.WrapFS(vfstest.FromMap(files, tspath.CaseSensitive))
	host := compiler.NewCompilerHost(fs, bundled.LibPath(), nil, nil, nil)
	parsed, errors := tsoptions.GetParsedCommandLineOfConfigFile("/tsconfig.json", &core.CompilerOptions{}, nil, fs, nil)
	assert.Equal(t, len(errors), 0, "Expected no errors in parsed command line")
	p := compiler.NewProgram(compiler.ProgramOptions{Config: parsed, Host: host})
	p.BindSourceFiles()
	file := p.GetSourceFile("/main.ts")
	var result ssc_result
	for sourceFile := range slices.Values(p.GetSourceFiles()) {
		for diagnostic := range slices.Values(p.GetSemanticDiagnostics(t.Context(), sourceFile)) {
			result.codes = append(result.codes, diagnostic.Code())
			result.messages = append(result.messages, ssc_messageTexts(diagnostic)...)
		}
	}
	for diagnostic := range slices.Values(p.GetSuggestionDiagnostics(t.Context(), file)) {
		result.suggestionCodes = append(result.suggestionCodes, diagnostic.Code())
	}
	c, done := p.GetTypeChecker(t.Context())
	defer done()
	for statement := range slices.Values(file.Statements.Nodes) {
		if statement.Kind != ast.KindVariableStatement {
			continue
		}
		for declaration := range slices.Values(statement.AsVariableStatement().DeclarationList.AsVariableDeclarationList().Declarations.Nodes) {
			if declaration.Name().Text() == "r" {
				result.resultType = c.TypeToString(c.GetTypeAtLocation(declaration.Name()))
			}
		}
	}
	return result
}

func ssc_checkSource(t *testing.T, source string) ssc_result {
	t.Helper()
	return ssc_check(t, map[string]string{
		"/node_modules/syscript/package.json": `{ "name": "syscript", "version": "0.0.0" }`,
		"/node_modules/syscript/prelude.d.ts": ssc_prelude,
		"/main.ts":                            ssc_declarations + source + "\nexport {};\n",
	}, []string{"node_modules/syscript/prelude.d.ts", "main.ts"})
}

func TestSscOperators(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name       string
		source     string
		resultType string
		codes      []int32
	}{
		{"same type", "const r = a32 + b32;", "i32", nil},
		{"literal right", "const r = a32 + 1;", "i32", nil},
		{"literal left", "const r = 1 + a32;", "i32", nil},
		{"literal union", "const r = a32 + lit;", "i32", nil},
		{"literal beyond operand range", "const r = ua8 + 300;", "u16", nil},
		{"literal union beyond operand range", "const r = ua8 + (c ? 1 : 300);", "u16", nil},
		{"negative literal with unsigned", "const r = ua32 + -1;", "i64", nil},
		{"large literal with unsigned", "const r = ua32 + 5000000000;", "u64", nil},
		{"literal beyond signed operand range", "const r = a8 + 300;", "i16", nil},
		{"mixed unions with literals", "const r = (c ? ua8 : a8) + (c ? 300 : -1);", "i16", nil},
		{"unsigned unions with literals", "const r = (c ? ua8 : ua32) + (c ? 1 : 5000000000);", "u64", nil},
		{"mixed unions with fractional literal", "const r = (c ? a32 : ua32) + (c ? 1 : 1.5);", "f64", nil},
		{"mixed unions without common type", "const r = (c ? ua64 : a8) + (c ? 1 : 2);", "any", []int32{2365}},
		{"signed union with unsigned literal beyond", "const r = (c ? a8 : a16) + (c ? 1 : 40000);", "i32", nil},
		{"fractional literal", "const r = ua8 + 1.5;", "f32", nil},
		{"fractional literal widening to f64", "const r = a32 + 1.5;", "f64", nil},
		{"fractional literal without common type", "const r = ua64 + 1.5;", "any", []int32{2365}},
		{"literal at i64 max", "const r = a64 + 9223372036854775807;", "i64", nil},
		{"literal beyond i64 without common type", "const r = a64 + 9223372036854775808;", "any", []int32{2365}},
		{"bitwise literal beyond operand range", "const r = ua8 & 0x1ff;", "u16", nil},
		{"bitwise fractional literal", "const r = ua8 & 1.5;", "any", []int32{2365}},
		{"relational literal beyond operand range", "const r = ua8 < 300;", "boolean", nil},
		{"compound assignment literal beyond range", "let v: u8 = 0;\nv += 300;", "", []int32{2322}},
		{"literal at u64 max with u64", "const r = ua64 + 18446744073709551615;", "u64", nil},
		{"literal over u64 without common type", "const r = ua64 + 18446744073709551616;", "any", []int32{2365}},
		{"compound assignment literal at u64 max", "let v: u64 = 0;\nv += 18446744073709551615;", "", nil},
		{"equality with negative literal widens to signed", "const r = ua8 === -1;", "boolean", nil},
		{"numeric enum", "const r = a32 * e;", "i32", nil},
		{"widen", "const r = a32 + a64;", "i64", nil},
		{"widen to third type", "const r = ua32 + a32;", "i64", nil},
		{"widen to f64", "const r = a32 * fa32;", "f64", nil},
		{"widen to f32", "const r = a16 * fa32;", "f32", nil},
		{"bitwise", "const r = a32 & b32;", "i32", nil},
		{"no common type", "const r = a64 + ua64;", "any", []int32{2365}},
		{"number as f64", "const r = a32 + n;", "f64", nil},
		{"number as f64 without common type", "const r = a64 + n;", "any", []int32{2365}},
		{"number only", "const r = n + n;", "f64", nil},
		{"literals only", "const r = 1 + 2;", "number", nil},
		{"string concatenation", `const r = a32 + "s";`, "string", nil},
		{"union operand", "const r = (c ? a32 : a64) + b32;", "i64", nil},
		{"union operands as a whole", "const r = (c ? ua32 : a8) + fa32;", "f64", nil},
		{"union without common type", "const r = (c ? a64 : ua64) + 1;", "any", []int32{2365}},
		{"unary", "const r = -a32;", "i32", nil},
		{"unary bitwise", "const r = ~a32;", "i32", nil},
		{"unary plus", "const r = +a32;", "f64", nil},
		{"unary plus on f32", "const r = +fa32;", "f64", nil},
		{"unary plus on u32 union", "const r = +(c ? ua32 : a32);", "f64", nil},
		{"unary plus lossy", "const r = +a64;", "any", []int32{2736}},
		{"unary plus on number", "const r = +n;", "f64", nil},
		{"unary union", "const r = -(c ? a32 : a64);", "i64", nil},
		{"unary union without common type", "const r = -(c ? a64 : ua64);", "any", []int32{2736}},
		{"unary minus on f32", "const r = -fa32;", "f32", nil},
		{"unary minus on u8 widens to signed", "const r = -ua8;", "i16", nil},
		{"unary minus on u32 widens to signed", "const r = -ua32;", "i64", nil},
		{"unary minus on u64 without signed type", "const r = -ua64;", "any", []int32{2736}},
		{"unary minus on mixed union", "const r = -(c ? ua8 : a8);", "i16", nil},
		{"unary bitwise on unsigned keeps type", "const r = ~ua8;", "u8", nil},
		{"increment on unsigned keeps type", "const r = ua8++;", "u8", nil},
		{"increment union without common type", "let v = c ? a64 : ua64;\nconst r = v++;", "any", []int32{2736}},
		{"prefix increment", "const r = ++a64;", "i64", nil},
		{"postfix increment", "const r = a32++;", "i32", nil},
		{"compound assignment", "a32 += 1;", "", nil},
		{"compound assignment narrowing", "a32 += a64;", "", []int32{2322}},
		{"relational", "const r = a32 < a64;", "boolean", nil},
		{"relational literal", "const r = a64 <= 1;", "boolean", nil},
		{"relational no common type", "const r = a64 < ua64;", "boolean", []int32{2365}},
		{"relational number", "const r = a32 < n;", "boolean", nil},
		{"relational number without common type", "const r = a64 < n;", "boolean", []int32{2365}},
		{"plus any", "const r = a32 + x;", "any", []int32{2365}},
		{"minus any", "const r = a32 - x;", "any", []int32{2365}},
		{"plus any with number", "const r = n + x;", "any", []int32{2365}},
		{"number with literal", "const r = n + 1;", "f64", nil},
		{"number with ssc", "const r = n - a32;", "f64", nil},
		{"unary minus on number", "const r = -n;", "f64", nil},
		{"f64 string concatenation", `const r = fa64 + "s";`, "string", nil},
		{"number string concatenation", `const r = n + "s";`, "string", nil},
		{"template literal", "const r = `${a64}`;", "string", nil},
		{"bigint operand", "const r = a32 + 1n;", "any", []int32{2365}},
		{"boolean operand", "const r = a32 + c;", "any", []int32{2365}},
		{"division", "const r = a32 / b32;", "i32", nil},
		{"remainder", "const r = a32 % b32;", "i32", nil},
		{"exponent", "const r = a32 ** 2;", "i32", nil},
		{"shift", "const r = a32 << 2;", "i32", nil},
		{"shift widening", "const r = a64 >> a32;", "i64", nil},
		{"unsigned shift", "const r = ua32 >>> 1;", "u32", nil},
		{"bitwise widening", "const r = a32 | ua32;", "i64", nil},
		{"bitwise to third type", "const r = a8 ^ ua8;", "i16", nil},
		{"bitwise on float", "const r = fa32 & fa32;", "any", []int32{2365}},
		{"bitwise widening to float", "const r = a32 | fa32;", "any", []int32{2365}},
		{"bitwise on number", "const r = a32 | n;", "any", []int32{2365}},
		{"bitwise number only", "const r = n | 0;", "any", []int32{2365}},
		{"shift on float", "const r = fa64 << 1;", "any", []int32{2365}},
		{"shift by float", "const r = a32 >> fa32;", "any", []int32{2365}},
		{"unary bitwise on float", "const r = ~fa32;", "any", []int32{2736}},
		{"unary bitwise on number", "const r = ~n;", "any", []int32{2736}},
		{"compound bitwise on float", "let v: f64 = 0;\nv |= 1;", "", []int32{2365}},
		{"compound bitwise on integer", "let v: u32 = 0;\nv |= 1;", "", nil},
		{"array length", "const r = items.length - 1;", "u64", nil},
		{"array length with signed", "const r = items.length + a32;", "any", []int32{2365}},
		{"loose equality", "const r = a32 == a64;", "boolean", nil},
		{"strict inequality without common type", "const r = a32 !== ua64;", "boolean", []int32{2367}},
		{"relational number left", "const r = n > a32;", "boolean", nil},
		{"relational to third type", "const r = ua32 >= a32;", "boolean", nil},
		{"compound assignment widening", "let v: i64 = 0;\nv += a32;", "", nil},
		{"compound multiply", "let v: i32 = 0;\nv *= 2;", "", nil},
		{"compound assignment number to f64", "let v: f64 = 0;\nv += n;", "", nil},
		{"compound assignment number to i32", "let v: i32 = 0;\nv += n;", "", []int32{2322}},
		{"loop over length", "for (let i: u64 = 0; i < items.length; i++) { items[i]; }", "", nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			result := ssc_checkSource(t, tc.source)
			if tc.resultType != "" {
				assert.Equal(t, result.resultType, tc.resultType)
			}
			assert.DeepEqual(t, result.codes, tc.codes)
		})
	}
}

func TestSscRelations(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name       string
		source     string
		resultType string
		codes      []int32
	}{
		{"lossless assignment", "const r: i64 = a32;", "i64", nil},
		{"lossless assignment across sign", "const r: i64 = ua32;", "i64", nil},
		{"lossy assignment", "const r: i32 = a64;", "i32", []int32{2322}},
		{"sign change", "const r: u32 = a32;", "u32", []int32{2322}},
		{"conditional keeps union", "const r = c ? a32 : a64;", "i32 | i64", nil},
		{"union assignment", "const r: i64 = c ? a32 : a64;", "i64", nil},
		{"multiple returns keep union", "function f() { return c ? a32 : a64; }\nconst r = f();", "i32 | i64", nil},
		{"generic inference", "declare function id<T>(v: T): T;\nconst r = id(a32 + a64);", "i64", nil},
		{"generic without implicit widening", "declare function pair<T>(p: T, q: T): T;\nconst r = pair(a32, a64);", "", []int32{2345}},
		{"equality with common type", "const r = ua32 === a32;", "boolean", nil},
		{"equality without common type", "const r = a64 === ua64;", "boolean", []int32{2367}},
		{"assertion with common type", "const r = a32 as i64;", "i64", nil},
		{"assertion without common type", "const r = ua64 as i64;", "i64", []int32{2352}},
		{"equality with literal beyond range", "const r = ua8 === 300;", "boolean", nil},
		{"equality with literal on the left", "const r = 300 === ua8;", "boolean", nil},
		{"equality with fractional literal without common type", "const r = ua64 === 1.5;", "boolean", []int32{2367}},
		{"assertion of literal beyond range", "const r = 300 as u8;", "u8", nil},
		{"assertion of literal in range", "const r = 1 as u64;", "u64", nil},
		{"assertion of literal at i64 max", "const r = 9223372036854775807 as i64;", "i64", nil},
		{"assertion of negative literal to unsigned", "const r = -1 as u64;", "u64", []int32{2352}},
		{"assertion of fractional literal to float", "const r = 1.5 as f32;", "f32", nil},
		{"assertion of literal union", "const r = (c ? 1 : 2) as u64;", "u64", nil},
		{"assertion of literal to literal is left to tsc", "const r = 1 as 2;", "2", nil},
		{"assertion of fractional literal without common type", "const r = 1.5 as u64;", "u64", []int32{2352}},
		{"switch case literal beyond range", "switch (ua8) { case 300: break; }", "", nil},
		{"switch case fractional literal without common type", "switch (ua64) { case 1.5: break; }", "", []int32{2678}},
		{"redeclaration", "var v: i32;\nvar v: i64;", "", []int32{2403}},
		{"number assignment", "const r: i32 = n;", "i32", []int32{2322}},
		{"number assignment to f64", "const r: f64 = n;", "f64", nil},
		{"number argument", "declare function take(v: i32): void;\ntake(n);", "", []int32{2345}},
		{"number return", "function f(): i32 { return n; }", "", []int32{2322}},
		{"union with number", "const r: i64 = c ? a32 : n;", "i64", []int32{2322}},
		{"literal assignment", "const r: i32 = 1;", "i32", nil},
		{"ssc to number", "const r: number = a32;", "number", nil},
		{"lossy ssc to number", "const r: number = a64;", "number", []int32{2322}},
		{"index with i64", "const r = items[a64];", "string", nil},
		{"index with u64", "const r = items[ua64];", "string", nil},
		{"index with i32", "const r = items[a32];", "string", nil},
		{"index with number", "const r = items[n];", "string", nil},
		{"length", "const r = items.length;", "u64", nil},
		{"compare unsigned with length", "const r = ua32 < items.length;", "boolean", nil},
		{"compare signed with length", "const r = a32 < items.length;", "boolean", []int32{2365}},
		{"index with union", "const r = items[c ? a32 : ua64];", "string", nil},
		{"object index with i64 signature", "declare const rec: { [n: i64]: string };\nconst r = rec[a64];", "string", nil},
		{"object index without i64 signature", "declare const rec: { [n: number]: string };\nconst r = rec[a64];", "any", []int32{7015}},
		{"lossy ssc as number argument", "const r = abs(a64);", "number", []int32{2345}},
		{"ssc as number argument", "const r = abs(a32);", "number", nil},
		{"number as assertion", "const r = n as i32;", "i32", nil},
		{"lossy assertion to number", "const r = a64 as number;", "number", []int32{2352}},
		{"array element widening", "const r: i64[] = [a32];", "i64[]", nil},
		{"lossy array element", "const r: i32[] = [a64];", "i32[]", []int32{2322}},
		{"number array element", "const r: i32[] = [n];", "i32[]", []int32{2322}},
		{"property widening", "const r: { v: i64 } = { v: a32 };", "", nil},
		{"lossy property", "const r: { v: i32 } = { v: a64 };", "", []int32{2322}},
		{"generic constraint", "declare function g<T extends i64>(v: T): T;\nconst r = g(a32);", "i32", nil},
		{"generic constraint violation", "declare function g<T extends i64>(v: T): T;\nconst r = g(ua64);", "", []int32{2345}},
		{"overload exact", "declare function o(v: i32): 'i32';\ndeclare function o(v: i64): 'i64';\nconst r = o(a64);", `"i64"`, nil},
		{"overload first match", "declare function o(v: i32): 'i32';\ndeclare function o(v: i64): 'i64';\nconst r = o(a32);", `"i32"`, nil},
		{"union target with string", "const r: i32 | string = a64;", "", []int32{2322}},
		{"optional target", "const r: i64 | undefined = a32;", "", nil},
		{"lossy optional target", "const r: i32 | undefined = a64;", "", []int32{2322}},
		{"switch case comparability", "switch (a32) { case a64: break; case ua64: break; }", "", []int32{2678}},
		{"tuple element", "const t: [string, i32] = ['a', a32];\nconst r = t[1];", "i32", nil},
		{"conditional with literal", "const r = c ? a32 : 1;", "1 | i32", nil},
		{"mutable conditional with literal", "let r = c ? a32 : 1;", "number | i32", nil},
		{"literals in array", "const r: i32[] = [1, 2];", "i32[]", nil},
		{"literal in object", "const r: { v: i32 } = { v: 1 };", "", nil},
		{"literals in tuple", "const r: [i32, i64] = [1, 2];", "[i32, i64]", nil},
		{"literals in array argument", "declare function take(v: i32[]): void;\ntake([1, 2]);", "", nil},
		{"typeof narrowing", "declare const ns: i32 | string;\nconst r = typeof ns === 'number' ? ns : a32;", "i32", nil},
		{"const object", "const r = { a: 1, b: a32, c: a32 + a64, d: n } as const;", "{ readonly a: 1; readonly b: i32; readonly c: i64; readonly d: number; }", nil},
		{"plain object", "const r = { a: 1, b: a32, c: a32 + a64, d: n };", "{ a: number; b: i32; c: i64; d: number; }", nil},
		{"const tuple", "const r = [1, a32, a64] as const;", "readonly [1, i32, i64]", nil},
		{"nested const object", "const r = { nested: { a: 1, b: ua8 } } as const;", "{ readonly nested: { readonly a: 1; readonly b: u8; }; }", nil},
		{"literal at u8 max", "const r: u8 = 255;", "u8", nil},
		{"literal over u8", "const r: u8 = 256;", "u8", []int32{2322}},
		{"negative literal to unsigned", "const r: u8 = -1;", "u8", []int32{2322}},
		{"literal at i8 min", "const r: i8 = -128;", "i8", nil},
		{"literal below i8", "const r: i8 = -129;", "i8", []int32{2322}},
		{"literal at i32 max", "const r: i32 = 2147483647;", "i32", nil},
		{"literal over i32", "const r: i32 = 2147483648;", "i32", []int32{2322}},
		{"literal at u32 max", "const r: u32 = 4294967295;", "u32", nil},
		{"fractional literal to integer", "const r: i32 = 1.5;", "i32", []int32{2322}},
		{"fractional literal to f64", "const r: f64 = 1.5;", "f64", nil},
		{"exact literal to f32", "const r: f32 = 0.5;", "f32", nil},
		{"inexact literal to f32", "const r: f32 = 0.1;", "f32", nil},
		{"literal argument out of range", "declare function take(v: u8): void;\ntake(300);", "", []int32{2345}},
		{"literal return out of range", "function f(): u8 { return 300; }", "", []int32{2322}},
		{"literal array element out of range", "const r: u8[] = [1, 300];", "u8[]", []int32{2322}},
		{"literal union out of range", "const r: u8 = c ? 1 : 300;", "u8", []int32{2322}},
		{"fractional literal to number is left to tsc", "const r: number = 1.5;", "number", nil},
		{"exponent literal to integer", "const r: i32 = 1e3;", "i32", nil},
		{"fractional exponent literal to integer", "const r: i32 = 1.5e1;", "i32", nil},
		{"negative exponent literal to integer", "const r: i32 = 1e-3;", "i32", []int32{2322}},
		{"hex literal at u8 max", "const r: u8 = 0xff;", "u8", nil},
		{"hex literal over u8", "const r: u8 = 0x100;", "u8", []int32{2322}},
		{"separated literal", "const r: i32 = 1_000_000;", "i32", nil},
		{"literal at i64 max", "const r: i64 = 9223372036854775807;", "i64", nil},
		{"literal over i64", "const r: i64 = 9223372036854775808;", "i64", []int32{2322}},
		{"literal at i64 min", "const r: i64 = -9223372036854775808;", "i64", nil},
		{"literal below i64", "const r: i64 = -9223372036854775809;", "i64", []int32{2322}},
		{"literal at u64 max", "const r: u64 = 18446744073709551615;", "u64", nil},
		{"literal over u64", "const r: u64 = 18446744073709551616;", "u64", []int32{2322}},
		{"literal over f32", "const r: f32 = 1e39;", "f32", nil},
		{"literal under f32", "const r: f32 = 1e-50;", "f32", nil},
		{"zero literal to f32", "const r: f32 = 0.0;", "f32", nil},
		{"literal over f64", "const r: f64 = 1e309;", "f64", nil},
		{"literal under f64", "const r: f64 = 1e-400;", "f64", nil},
		{"literal over number", "const r: number = 1e309;", "number", nil},
		{"literal in optional ssc target", "const r: u8 | undefined = 300;", "", []int32{2322}},
		{"hex literal at u64 max", "const r: u64 = 0xffffffffffffffff;", "u64", nil},
		{"hex literal over u64", "const r: u64 = 0x1_0000_0000_0000_0000;", "u64", []int32{2322}},
		{"binary literal at u8 max", "const r: u8 = 0b1111_1111;", "u8", nil},
		{"binary literal over u8", "const r: u8 = 0b1_0000_0000;", "u8", []int32{2322}},
		{"octal literal at u8 max", "const r: u8 = 0o377;", "u8", nil},
		{"octal literal over u8", "const r: u8 = 0o400;", "u8", []int32{2322}},
		{"separated literal at i64 min", "const r: i64 = -9_223_372_036_854_775_808;", "i64", nil},
		{"negative zero to unsigned", "const r: u8 = -0;", "u8", nil},
		{"plus literal at u8 max", "const r: u8 = +255;", "u8", nil},
		{"plus literal over u8", "const r: u8 = +300;", "u8", []int32{2322}},
		{"negated parenthesized literal is f64", "const r: i8 = -(128);", "i8", []int32{2322}},
		{"integer valued fraction", "const r: u8 = 255.0;", "u8", nil},
		{"integer valued exponent", "const r: u8 = 2.55e2;", "u8", nil},
		{"large literal through a variable", "const a = 9223372036854775807;\nconst r: i64 = a;", "i64", nil},
		{"large literal over i64 through a variable", "const a = 9223372036854775808;\nconst r: i64 = a;", "i64", []int32{2322}},
		{"same large literals", "const r = 9223372036854775807 === 9223372036854775807;", "boolean", nil},
		{"large literals rounding to the same float", "const r = 9223372036854775807 === 9223372036854775806;", "boolean", []int32{2367}},
		{"literal over range in object", "const r: { v: u8 } = { v: 300 };", "", []int32{2322}},
		{"literal over range in tuple", "const r: [i32, u8] = [1, 300];", "", []int32{2322}},
		{"literal over range as default parameter", "function f(v: u8 = 300) { return v; }", "", []int32{2322}},
		{"literal at u64 max as return", "function f(): u64 { return 18446744073709551615; }", "", nil},
		{"literal over range to optional parameter", "declare function f(v?: u8): void;\nf(300);", "", []int32{2345}},
		{"literal over range to generic constraint", "declare function g<T extends u8>(v: T): T;\nconst r = g(300);", "", []int32{2345}},
		{"overload skips a literal over range", "declare function f(v: u8): u8;\ndeclare function f(v: u16): u16;\nconst r = f(300);", "u16", nil},
		{"overload takes a literal in range", "declare function f(v: u8): u8;\ndeclare function f(v: u16): u16;\nconst r = f(255);", "u8", nil},
		{"overload with a literal operand over range", "declare function f(p: u8, q: u8): u8;\ndeclare function f(p: u16, q: u16): u16;\nconst r = f(ua8, 300);", "u16", nil},
		{"overload without a literal in range", "declare function f(v: u8): u8;\ndeclare function f(v: i8): i8;\nconst r = f(300);", "", []int32{2769}},
		{"returns keep a literal over range", "function f() { if (c) { return ua8; } return 300; }\nconst r = f();", "300 | u8", nil},
		{"huge literal without context", "const r = 1e1000;", "", nil},
		{"tiny literal without context", "const r = 1e-1000;", "", nil},
		{"negative huge literal without context", "const r = -1e1000;", "", nil},
		{"plus huge literal without context", "const r = +1e1000;", "", nil},
		{"huge literal in integer context", "const r: u64 = 1e400;", "u64", []int32{2322}},
		{"huge exponent literal in integer context", "const r: u64 = 1e1000;", "u64", []int32{2322}},
		{"tiny exponent literal in integer context is 0", "const r: i32 = 1e-1000;", "i32", nil},
		{"long mantissa with large negative exponent", "const r = 10000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000e-800;", "", nil},
		{"never assignment", "declare const nv: never;\nconst r: i32 = nv;", "i32", nil},
		{"any assignment is left to tsc", "const r: i32 = x;", "i32", nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			result := ssc_checkSource(t, tc.source)
			if tc.resultType != "" {
				assert.Equal(t, result.resultType, tc.resultType)
			}
			assert.DeepEqual(t, result.codes, tc.codes)
		})
	}
}

func TestSscLargeLiteralSuggestion(t *testing.T) {
	t.Parallel()
	t.Run("suppressed in ssc mode", func(t *testing.T) {
		t.Parallel()
		result := ssc_checkSource(t, "const r: u64 = 18446744073709551615;")
		assert.Assert(t, !slices.Contains(result.suggestionCodes, 80008))
	})
	t.Run("reported without prelude", func(t *testing.T) {
		t.Parallel()
		result := ssc_check(t, map[string]string{
			"/main.ts": "const r = 18446744073709551615;\nexport {};\n",
		}, []string{"main.ts"})
		assert.Assert(t, slices.Contains(result.suggestionCodes, 80008))
	})
}

func TestSscPreludeIdentification(t *testing.T) {
	t.Parallel()
	const localAliases = `type i32 = number & {};
type u32 = number & {};
declare const p: i32;
declare const q: u32;
const r = p + q;
export {};
`
	t.Run("same name aliases without prelude", func(t *testing.T) {
		t.Parallel()
		result := ssc_check(t, map[string]string{
			"/node_modules/other/package.json": `{ "name": "other", "version": "0.0.0" }`,
			"/node_modules/other/prelude.d.ts": ssc_prelude,
			"/main.ts":                         localAliases,
		}, []string{"node_modules/other/prelude.d.ts", "main.ts"})
		assert.Equal(t, result.resultType, "number")
		assert.DeepEqual(t, result.codes, []int32(nil))
	})
	t.Run("partial prelude disables ssc mode", func(t *testing.T) {
		t.Parallel()
		cases := []struct {
			name       string
			source     string
			resultType string
			codes      []int32
		}{
			{"lossy assignment", "declare const q: i64;\nconst r: i32 = q;", "i32", []int32{2322}},
			{"unary plus", "declare const p: i32;\nconst r = +p;", "number", nil},
			{"literal in ssc context", "const o = { v: 1 } satisfies { v: i32 };\nconst r = o.v;", "number", []int32{2322}},
			{"huge literal", "const r = 1e1000;", "Infinity", nil},
			{"literal over i64", "const r: i64 = 9223372036854775808;", "i64", []int32{2322}},
		}
		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				t.Parallel()
				result := ssc_check(t, map[string]string{
					"/node_modules/syscript/package.json": `{ "name": "syscript", "version": "0.0.0" }`,
					"/node_modules/syscript/prelude.d.ts": strings.Replace(ssc_prelude, "  type f32 = number & { readonly [ssc]: 'f32' };\n", "", 1),
					"/main.ts":                            tc.source + "\nexport {};\n",
				}, []string{"node_modules/syscript/prelude.d.ts", "main.ts"})
				assert.Equal(t, result.resultType, tc.resultType)
				assert.DeepEqual(t, result.codes, tc.codes)
			})
		}
	})
	t.Run("prelude outside the syscript package", func(t *testing.T) {
		t.Parallel()
		result := ssc_check(t, map[string]string{
			"/node_modules/other/package.json": `{ "name": "other", "version": "0.0.0" }`,
			"/node_modules/other/prelude.d.ts": ssc_prelude,
			"/main.ts":                         ssc_declarations + "const r = a64 + ua64;\nexport {};\n",
		}, []string{"node_modules/other/prelude.d.ts", "main.ts"})
		assert.Equal(t, result.resultType, "number")
		assert.DeepEqual(t, result.codes, []int32(nil))
	})
	t.Run("value merged into a prelude type", func(t *testing.T) {
		t.Parallel()
		result := ssc_check(t, map[string]string{
			"/user.d.ts":                          "declare function i32(): void;\n",
			"/node_modules/syscript/package.json": `{ "name": "syscript", "version": "0.0.0" }`,
			"/node_modules/syscript/prelude.d.ts": ssc_prelude,
			"/main.ts":                            ssc_declarations + "const r = a32 + b32;\nexport {};\n",
		}, []string{"user.d.ts", "node_modules/syscript/prelude.d.ts", "main.ts"})
		assert.Equal(t, result.resultType, "i32")
		assert.DeepEqual(t, result.codes, []int32(nil))
	})
}

func TestSscBrand(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name       string
		source     string
		resultType string
		codes      []int32
	}{
		{"identity survives generic inference", "declare function id<T>(v: T): T;\nconst r = id(a32) + b32;", "i32", nil},
		{"identity survives a user alias", "type MyInt = i32;\ndeclare const m: MyInt;\nconst r = m + ua8;", "i32", nil},
		{"different brands do not intersect", "declare const both: i32 & u8;\nconst r = both;", "never", nil},
		{"the brand symbol is not visible outside the prelude", "declare const fake: number & { readonly [ssc]: 'i32' };\nconst r = 0;", "", []int32{2304}},
		{"number is not a syscript integer", "const r: i32 = n;", "i32", []int32{2322}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			result := ssc_checkSource(t, tc.source)
			if tc.resultType != "" {
				assert.Equal(t, result.resultType, tc.resultType)
			}
			assert.DeepEqual(t, result.codes, tc.codes)
		})
	}
}

func TestSscHookCoverage(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name       string
		source     string
		resultType string
		codes      []int32
		messages   []string
	}{
		{"unary plus keeps a large literal exact", "const r: u64 = +18446744073709551615;", "u64", nil, nil},
		{"large literal is shown exactly", "const r = 18446744073709551615;", "18446744073709551615", nil, nil},
		{"negative large literal is shown exactly", "const r = -9223372036854775808;", "-9223372036854775808", nil, nil},
		{"comparison error shows the literal", "const r = ua64 === -1;", "boolean", []int32{2367}, []string{"This comparison appears to be unintentional because the types 'u64' and '-1' have no overlap."}},
		{"operator error shows the literal", "const r = ua8 + 'x' - 300;", "", []int32{2362}, nil},
		{"widening through an empty object intersection", "declare const v: i32 & {};\nconst r: i64 = v;", "i64", nil, nil},
		{"widening through a number intersection", "declare const w: i32 & number;\nconst r: i64 = w;", "i64", nil, nil},
		{"widening a union argument", "declare function f(v: i64): void;\nf(c ? a32 : ua8);\nconst r = 0;", "", nil, nil},
		{"widening a conditional type", "type T<X> = X extends i32 ? X : never;\ndeclare const tc: T<i32>;\nconst r: i64 = tc;", "i64", nil, nil},
		{"widening an indexed access", "declare const o: { v: i32 };\nconst r: i64 = o['v'];", "i64", nil, nil},
		{"widening a NoInfer substitution", "type NoInfer<T> = intrinsic;\ndeclare const ni: NoInfer<i32>;\nconst r: i64 = ni;", "i64", nil, nil},
		{"lossy NoInfer substitution", "type NoInfer<T> = intrinsic;\ndeclare const nl: NoInfer<i64>;\nconst r: i32 = nl;", "i32", []int32{2322}, nil},
		{"widening a generic constraint", "function g<T extends i32>(v: T) {\n  const inner: i64 = v;\n}\nconst r = 0;", "", nil, nil},
		{"widening an unknown intersection", "declare const ui: i32 & unknown;\nconst r: i64 = ui;", "i64", nil, nil},
		{"widening a readonly array element", "declare const ra: readonly i32[];\nconst r: readonly i64[] = ra;", "", nil, nil},
		{"widening a function return", "declare const fr: () => i32;\nconst r: () => i64 = fr;", "() => i64", nil, nil},
		{"lossy function parameter", "declare const fp: (v: i32) => void;\nconst r: (v: i64) => void = fp;", "(v: i64) => void", []int32{2322}, nil},
		{"literal in range to an optional target", "const r: u8 | undefined = 7;", "", nil, nil},
		{"literal over range to an optional target shows the literal", "const r: u8 | undefined = 300;", "", []int32{2322}, []string{"Type '300' is not assignable to type 'u8 | undefined'."}},
		{"literal over range to an optional parameter shows the literal", "declare function opt(v?: u8): void;\nopt(300);\nconst r = 0;", "", []int32{2345}, []string{"Argument of type '300' is not assignable to parameter of type 'u8 | undefined'."}},
		{"number target takes a lossless integer", "const r: number = a32;", "number", nil, nil},
		{"number target rejects a lossy integer", "const r: number = ua64;", "number", []int32{2322}, []string{"Type 'u64' is not assignable to type 'number'."}},
		{"number target takes a large literal", "const r: number = 18446744073709551615;", "number", nil, nil},
		{"comparison with number uses f64", "const r = a32 === n;", "boolean", nil, nil},
		{"comparison with number rejects a lossy integer", "const r = ua64 === n;", "boolean", []int32{2367}, nil},
		{"char assignment error shows the literal", "const r: char = 'AB';", "char", []int32{2322}, []string{"Type '\"AB\"' is not assignable to type 'char'."}},
		{"char lone surrogate error shows the literal", "const r: char = '\\uD800';", "char", []int32{2322}, []string{"Type '\"\\uD800\"' is not assignable to type 'char'."}},
		{"char argument error shows the literal", "declare function f(v: char): void;\nf('AB');\nconst r = 0;", "", []int32{2345}, []string{"Argument of type '\"AB\"' is not assignable to parameter of type 'char'."}},
		{"char comparison error shows the literal", "const r = ch === 'AB';", "boolean", []int32{2367}, []string{"This comparison appears to be unintentional because the types 'char' and '\"AB\"' have no overlap."}},
		{"lossy assignment message names both types", "const r: i32 = a64;", "i32", []int32{2322}, []string{"Type 'i64' is not assignable to type 'i32'."}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			result := ssc_checkSource(t, tc.source)
			if tc.resultType != "" {
				assert.Equal(t, result.resultType, tc.resultType)
			}
			assert.DeepEqual(t, result.codes, tc.codes)
			if tc.messages != nil {
				assert.DeepEqual(t, result.messages, tc.messages)
			}
		})
	}
}

func TestSscChar(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name       string
		source     string
		resultType string
		codes      []int32
	}{
		{"widens to u32", "const r: u32 = ch;", "u32", nil},
		{"widens to u64", "const r: u64 = ch;", "u64", nil},
		{"widens to i64", "const r: i64 = ch;", "i64", nil},
		{"widens to f64", "const r: f64 = ch;", "f64", nil},
		{"widens to number", "const r: number = ch;", "number", nil},
		{"does not narrow to u16", "const r: u16 = ch;", "u16", []int32{2322}},
		{"does not narrow to i32", "const r: i32 = ch;", "i32", []int32{2322}},
		{"u32 is not a char", "const r: char = ua32;", "char", []int32{2322}},
		{"number literal is not a char", "const r: char = 65;", "char", []int32{2322}},
		{"arithmetic computes as u32", "const r = ch + 1;", "u32", nil},
		{"arithmetic of two chars computes as u32", "const r = ch + ch2;", "u32", nil},
		{"arithmetic with a wider integer", "const r = ch + ua64;", "u64", nil},
		{"negation computes as i64", "const r = -ch;", "i64", nil},
		{"bitwise computes as u32", "const r = ~ch;", "u32", nil},
		{"comparison between chars", "const r = ch === ch2;", "boolean", nil},
		{"comparison with an integer", "const r = ch < ua32;", "boolean", nil},
		{"conditional keeps char", "const r = c ? ch : ch2;", "char", nil},
		{"compound assignment does not produce a char", "ch += 1;\nconst r = 0;", "", []int32{2322}},
		{"increment does not produce a char", "ch++;\nconst r = 0;", "", []int32{2736}},
		{"decrement does not produce a char", "--ch;\nconst r = 0;", "", []int32{2736}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			result := ssc_checkSource(t, tc.source)
			if tc.resultType != "" {
				assert.Equal(t, result.resultType, tc.resultType)
			}
			assert.DeepEqual(t, result.codes, tc.codes)
		})
	}
}

func TestSscCharLiteral(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name       string
		source     string
		resultType string
		codes      []int32
	}{
		{"ascii literal", "const r: char = 'A';", "char", nil},
		{"three byte literal", "const r: char = '한';", "char", nil},
		{"four byte literal", "const r: char = '👍';", "char", nil},
		{"code point escape", "const r: char = '\\u{1F44D}';", "char", nil},
		{"surrogate pair escape", "const r: char = '\\uD83D\\uDC4D';", "char", nil},
		{"literal in a let", "let r: char = 'A';", "char", nil},
		{"literal as an argument", "declare function f(v: char): void;\nf('A');\nconst r = 0;", "", nil},
		{"literal to an optional target", "const r: char | undefined = 'A';", "", nil},
		{"literal widens to u32", "const r: u32 = 'A';", "u32", nil},
		{"literal widens to u64", "const r: u64 = 'A';", "u64", nil},
		{"literal widens to i64", "const r: i64 = 'A';", "i64", nil},
		{"literal widens to f64", "const r: f64 = 'A';", "f64", nil},
		{"literal widens as an argument", "declare function f(v: u32): void;\nf('A');\nconst r = 0;", "", nil},
		{"literal does not narrow to u16", "const r: u16 = 'A';", "u16", []int32{2322}},
		{"literal does not narrow to i32", "const r: i32 = 'A';", "i32", []int32{2322}},
		{"two code points do not widen to u32", "const r: u32 = 'AB';", "u32", []int32{2322}},
		{"two code points", "const r: char = 'AB';", "char", []int32{2322}},
		{"empty literal", "const r: char = '';", "char", []int32{2322}},
		{"grapheme of two code points", "const r: char = '👍🏽';", "char", []int32{2322}},
		{"lone surrogate", "const r: char = '\\uD800';", "char", []int32{2322}},
		{"two code points as an argument", "declare function f(v: char): void;\nf('AB');\nconst r = 0;", "", []int32{2345}},
		{"string is not a char", "declare const s: string;\nconst r: char = s;", "char", []int32{2322}},
		{"equality with a literal", "const r = ch === 'A';", "boolean", nil},
		{"literal on the left", "const r = 'A' === ch;", "boolean", nil},
		{"ordering with a literal", "const r = ch < 'Z';", "boolean", nil},
		{"switch case literal", "switch (ch) {\n  case 'A':\n    break;\n}\nconst r = 0;", "", nil},
		{"equality with two code points", "const r = ch === 'AB';", "boolean", []int32{2367}},
		{"ordering with two code points", "const r = ch < 'AB';", "", []int32{2365}},
		{"arithmetic with a literal", "const r = ch - 'A';", "", []int32{2363}},
		{"literal without a char is not a number", "const r = 'A' * 2;", "", []int32{2362}},
		{"concatenation stays a string", "const r = ch + 'A';", "string", nil},
		{"assertion of a char literal", "const r = 'A' as char;", "char", nil},
		{"assertion of two code points", "const r = 'AB' as char;", "char", []int32{2352}},
		{"assertion of a literal to a number type", "const r = 'A' as u8;", "u8", []int32{2352}},
		{"comparison of a literal with a number type", "const r = ua8 === 'A';", "boolean", []int32{2367}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			result := ssc_checkSource(t, tc.source)
			if tc.resultType != "" {
				assert.Equal(t, result.resultType, tc.resultType)
			}
			assert.DeepEqual(t, result.codes, tc.codes)
		})
	}
}
