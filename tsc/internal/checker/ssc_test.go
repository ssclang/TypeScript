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
  interface Array<T> { length: u64; [n: number]: T }
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
		{"different types", "const r = a32 + a64;", "any", []int32{2365}},
		{"literal is a number", "const r = a32 + 1;", "any", []int32{2365}},
		{"literal with f64", "const r = fa64 + 1;", "any", []int32{2365}},
		{"number with f64", "const r = fa64 + n;", "any", []int32{2365}},
		{"f64 with f64", "const r = fa64 * fa64;", "f64", nil},
		{"number with i32", "const r = a32 + n;", "any", []int32{2365}},
		{"literals only", "const r = 1 + 2;", "number", nil},
		{"literal variable stays a literal", "const r = 1;", "1", nil},
		{"number only", "const r = n + n;", "number", nil},
		{"constructor result", "declare function i32(v: number): i32;\nconst r = a32 + i32(1);", "i32", nil},
		{"same type union", "const r = (c ? a32 : b32) + a32;", "i32", nil},
		{"mixed union", "const r = (c ? a32 : a64) + a32;", "any", []int32{2365}},
		{"string concatenation", `const r = a32 + "s";`, "string", nil},
		{"template literal", "const r = `${a64}`;", "string", nil},
		{"bigint operand", "const r = a32 + 1n;", "any", []int32{2365}},
		{"boolean operand", "const r = a32 + c;", "any", []int32{2365}},
		{"any operand", "const r = a32 + x;", "any", []int32{2365}},
		{"numeric enum", "const r = a32 * e;", "any", []int32{2365}},
		{"number intersection with number", "declare const w: number & {};\nconst r = w + n;", "number", nil},
		{"computed enum with number", "enum F { A = abs(1) }\ndeclare const f: F;\nconst r = f + n;", "number", nil},
		{"computed enum with ssc", "enum F { A = abs(1) }\ndeclare const f: F;\nconst r = f + a32;", "any", []int32{2365}},
		{"division", "const r = a32 / b32;", "i32", nil},
		{"remainder", "const r = a32 % b32;", "i32", nil},
		{"bitwise", "const r = a32 & b32;", "i32", nil},
		{"shift", "const r = a32 << b32;", "i32", nil},
		{"unsigned shift", "const r = ua32 >>> ua32;", "u32", nil},
		{"bitwise on float", "const r = fa32 & fa32;", "any", []int32{2365}},
		{"bitwise on number like JS", "const r = n | 0;", "number", nil},
		{"bitwise on f64", "const r = fa64 & fa64;", "any", []int32{2365}},
		{"shift on f64", "const r = fa64 >>> fa64;", "any", []int32{2365}},
		{"enum bitwise", "const r = E.A | E.B;", "number", nil},
		{"unary minus", "const r = -a32;", "i32", nil},
		{"unary minus on unsigned", "const r = -ua8;", "any", []int32{2736}},
		{"unary minus on f32", "const r = -fa32;", "f32", nil},
		{"unary plus keeps type", "const r = +a32;", "i32", nil},
		{"unary bitwise", "const r = ~ua8;", "u8", nil},
		{"unary bitwise on f32", "const r = ~fa32;", "any", []int32{2736}},
		{"unary bitwise on f64", "const r = ~fa64;", "any", []int32{2736}},
		{"unary minus on number", "const r = -n;", "number", nil},
		{"unary bitwise on number", "const r = ~n;", "number", nil},
		{"prefix increment", "const r = ++a64;", "i64", nil},
		{"postfix increment", "const r = a32++;", "i32", nil},
		{"compound assignment", "a32 += b32;", "", nil},
		{"compound assignment with literal", "a32 += 1;", "", []int32{2365}},
		{"compound assignment with other type", "a64 += a32;", "", []int32{2365}},
		{"relational", "const r = a32 < b32;", "boolean", nil},
		{"relational different types", "const r = a32 < a64;", "boolean", []int32{2365}},
		{"relational literal", "const r = a32 < 1;", "boolean", []int32{2365}},
		{"relational f64 literal", "const r = fa64 < 1;", "boolean", []int32{2365}},
		{"equality same type", "const r = a32 === b32;", "boolean", nil},
		{"equality different types", "const r = a32 === a64;", "boolean", []int32{2367}},
		{"array length", "const r = items.length - ua64;", "u64", nil},
		{"compare with length", "const r = ua64 < items.length;", "boolean", nil},
		{"logical and same type", "const r = a32 && b32;", "i32", nil},
		{"logical or different types", "const r = a32 || a64;", "any", []int32{2365}},
		{"logical and boolean left", "const r = c && a32;", "false | i32", nil},
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
		{"same type assignment", "const r: i32 = a32;", "i32", nil},
		{"no widening", "const r: i64 = a32;", "i64", []int32{2322}},
		{"no narrowing", "const r: i32 = a64;", "i32", []int32{2322}},
		{"literal assignment", "const r: i32 = 1;", "i32", []int32{2322}},
		{"literal to f64", "const r: f64 = 1.5;", "f64", []int32{2322}},
		{"number assignment", "const r: i32 = n;", "i32", []int32{2322}},
		{"number to f64", "const r: f64 = n;", "f64", []int32{2322}},
		{"f64 to number", "const r: number = fa64;", "number", []int32{2322}},
		{"literal argument", "declare function take(v: i32): void;\ntake(1);", "", []int32{2345}},
		{"literal return", "function f(): i32 { return 0; }", "", []int32{2322}},
		{"literal array element", "const r: i32[] = [1];", "i32[]", []int32{2322}},
		{"literal as assertion", "const r = 1 as i32;", "i32", []int32{2352}},
		{"number as assertion", "const r = n as i32;", "i32", []int32{2352}},
		{"ssc as number assertion", "const r = a32 as number;", "number", []int32{2352}},
		{"ssc as same ssc assertion", "const r = a32 as i32;", "i32", nil},
		{"ssc equality with number", "const r = a32 === n;", "boolean", []int32{2367}},
		{"number equality with ssc", "const r = n === a32;", "boolean", []int32{2367}},
		{"ssc equality with literal", "const r = a32 === 1;", "boolean", []int32{2367}},
		{"ssc union equality with literal", "declare const v: i32 | undefined;\nconst r = v === 1;", "boolean", []int32{2367}},
		{"ssc equality with same ssc", "const r = a32 === b32;", "boolean", nil},
		{"ssc switch case with literal", "switch (a32) { case 1: break; }", "", []int32{2678}},
		{"ssc switch case with same ssc", "switch (a32) { case b32: break; }", "", nil},
		{"ssc to enum", "const r: E = a32;", "E", []int32{2322}},
		{"ssc to computed enum intersection", "enum F { A = abs(1) }\nconst r: F & {} = a32;", "", []int32{2322}},
		{"ssc to computed enum", "enum F { A = abs(1) }\nconst r: F = a32;", "", []int32{2322}},
		{"ssc equality with enum member", "const r = a32 === E.A;", "boolean", []int32{2367}},
		{"ssc to number", "const r: number = a32;", "number", []int32{2322}},
		{"ssc assignment to number", "let r: number = n;\nr = a32;", "", []int32{2322}},
		{"ssc return as number", "function f(): number { return a32; }", "", []int32{2322}},
		{"ssc argument as number", "declare function take(v: number): void;\ntake(a32);", "", []int32{2345}},
		{"ssc array as number array", "declare const ints: i32[];\nconst r: number[] = ints;", "number[]", []int32{2322}},
		{"ssc property as number", "const r: { x: number } = { x: a32 };", "", []int32{2322}},
		{"ssc object as number object", "declare const p: { x: i32 };\nconst r: { x: number } = p;", "", []int32{2322}},
		{"ssc to number union", "const r: number | undefined = a32;", "", []int32{2322}},
		{"ssc union to number union", "declare const v: i32 | undefined;\nconst r: number | undefined = v;", "", []int32{2322}},
		{"ssc generic constraint", "function g<T extends number>(v: T): T { return v; }\nconst r = g(a32);", "", []int32{2345}},
		{"ssc to union with same type", "const r: i32 | number = a32;", "number | i32", nil},
		{"number to number", "const r: number = n;", "number", nil},
		{"ssc to same ssc intersection", "declare function twice(v: i32 & {}): void;\ntwice(a32);", "", nil},
		{"ssc to number intersection", "const r: number & {} = a32;", "", []int32{2322}},
		{"constructor", "declare function i32(v: number): i32;\nconst r: i32 = i32(1);", "i32", nil},
		{"constructor from ssc", "declare function i32(v: i64 | number | string): i32;\nconst r: i32 = i32(a64);", "i32", nil},
		{"number constructor from ssc", "declare function num(v: i32 | number | string): number;\nconst r: number = num(a32);", "number", nil},
		{"template with ssc", "const r = `${a64}`;", "string", nil},
		{"conditional keeps union", "const r = c ? a32 : a64;", "i32 | i64", nil},
		{"index with u64", "const r = items[ua64];", "string", nil},
		{"length", "const r = items.length;", "u64", nil},
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
			{"different types add as numbers", "declare const p: i32;\ndeclare const q: i64;\nconst r = p + q;", "number", nil},
			{"unary plus", "declare const p: i32;\nconst r = +p;", "number", nil},
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
		{"identity survives a user alias", "type MyInt = i32;\ndeclare const m: MyInt;\nconst r = m + a32;", "i32", nil},
		{"different brands do not intersect", "declare const both: i32 & u8;\nconst r = both;", "never", nil},
		{"the brand symbol is not visible outside the prelude", "declare const fake: number & { readonly [ssc]: 'i32' };\nconst r = 0;", "", []int32{2304}},
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

func TestSscChar(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name       string
		source     string
		resultType string
		codes      []int32
	}{
		{"comparison between chars", "const r = ch === ch2;", "boolean", nil},
		{"ordering between chars", "const r = ch < ch2;", "boolean", nil},
		{"conditional keeps char", "const r = c ? ch : ch2;", "char", nil},
		{"arithmetic like an unsigned integer", "const r = ch + ch2;", "char", nil},
		{"bitwise like an unsigned integer", "const r = ch & ch2;", "char", nil},
		{"negation like an unsigned integer", "const r = -ch;", "any", []int32{2736}},
		{"increment", "const r = ch++;", "char", nil},
		{"no mixing with u32", "const r = ch + ua32;", "any", []int32{2365}},
		{"string literal is not a char", "const r: char = 'A';", "char", []int32{2322}},
		{"no widening to u32", "const r: u32 = ch;", "u32", []int32{2322}},
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
