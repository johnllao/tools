// Program expr demonstrates the expr-lang/expr expression evaluation library.
//
// expr-lang/expr evaluates expressions written in a simple, Go-like syntax
// against a Go environment (variables and functions). It is safe, fast, and
// type-checked at compile time.
//
// Usage:
//
//	go run ./examples/expr
package main

import (
	"fmt"

	"github.com/expr-lang/expr"
)

func main() {
	basicArithmetic()
	envVariables()
	booleans()
	ternary()
	strings()
	arraysAndMaps()
	customFunctions()
	compileTimeTypeCheck()
	letBindings()
	structEnv()
}

// ---------------------------------------------------------------------------
// 1. Basic arithmetic — no environment needed
// ---------------------------------------------------------------------------

func basicArithmetic() {
	fmt.Println("=== 1. Basic arithmetic ===")

	program, _ := expr.Compile(`42 * 2 + 10`)
	output, _ := expr.Run(program, nil)
	fmt.Printf("  42 * 2 + 10 = %v\n", output) // 94

	// Compile and run in one step with expr.Eval.
	out, _ := expr.Eval(`(1 + 2) * 3`, nil)
	fmt.Printf("  (1 + 2) * 3 = %v\n", out) // 9
}

// ---------------------------------------------------------------------------
// 2. Using environment variables
// ---------------------------------------------------------------------------

func envVariables() {
	fmt.Println("\n=== 2. Variables from an environment ===")

	env := map[string]interface{}{
		"price":   100.0,
		"taxRate": 0.08,
	}

	program, _ := expr.Compile(`price * (1 + taxRate)`, expr.Env(env))
	out, _ := expr.Run(program, env)
	fmt.Printf("  price * (1 + taxRate) = %v\n", out) // 108

	// Store the computed result in the environment so other expressions
	// can reference it as a value (not a formula string).
	env["pay"] = out

	program, _ = expr.Compile(`pay * 1`, expr.Env(env))
	out, _ = expr.Run(program, env)
	fmt.Printf("  pay * 1 = %v\n", out) // 108
}

// ---------------------------------------------------------------------------
// 3. Boolean expressions
// ---------------------------------------------------------------------------

func booleans() {
	fmt.Println("\n=== 3. Boolean expressions ===")

	env := map[string]interface{}{
		"age":   17,
		"limit": 18,
	}

	program, _ := expr.Compile(`age >= limit`, expr.Env(env))
	out, _ := expr.Run(program, env)
	fmt.Printf("  age >= limit => %v\n", out) // false

	// Multiple conditions with and/or/not.
	env["age"] = 21
	program, _ = expr.Compile(`age >= limit and age < 65`, expr.Env(env))
	out, _ = expr.Run(program, env)
	fmt.Printf("  age >= limit and age < 65 => %v\n", out) // true
}

// ---------------------------------------------------------------------------
// 4. Ternary / conditional operator
// ---------------------------------------------------------------------------

func ternary() {
	fmt.Println("\n=== 4. Ternary expressions ===")

	env := map[string]interface{}{
		"score": 85,
	}
	program, _ := expr.Compile(`score >= 60 ? "pass" : "fail"`, expr.Env(env))
	out, _ := expr.Run(program, env)
	fmt.Printf("  score >= 60 ? pass : fail => %v\n", out) // "pass"

	env["score"] = 45
	out, _ = expr.Run(program, env)
	fmt.Printf("  score >= 60 ? pass : fail => %v (score=45)\n", out) // "fail"
}

// ---------------------------------------------------------------------------
// 5. String operations
// ---------------------------------------------------------------------------

func strings() {
	fmt.Println("\n=== 5. Strings ===")

	env := map[string]interface{}{
		"name":   "claude",
		"prefix": "hello, ",
	}
	program, _ := expr.Compile(`prefix + name`, expr.Env(env))
	out, _ := expr.Run(program, env)
	fmt.Printf("  prefix + name => %v\n", out) // "hello, claude"

	// The built-in len() and in operators are available too.
	env = map[string]interface{}{
		"msg":      "hello",
		"wordlist": []string{"hi", "hello", "hey"},
	}
	program, _ = expr.Compile(`len(msg)`, expr.Env(env))
	out, _ = expr.Run(program, env)
	fmt.Printf("  len(msg) => %v\n", out) // 5

	program, _ = expr.Compile(`msg in wordlist`, expr.Env(env))
	out, _ = expr.Run(program, env)
	fmt.Printf("  msg in wordlist => %v\n", out) // true
}

// ---------------------------------------------------------------------------
// 6. Arrays, slices, and maps
// ---------------------------------------------------------------------------

func arraysAndMaps() {
	fmt.Println("\n=== 6. Arrays and maps ===")

	env := map[string]interface{}{
		"items": []int{10, 20, 30, 40},
		"meta": map[string]interface{}{
			"version": 2,
			"active":  true,
		},
	}

	// Indexing and slice expressions.
	program, _ := expr.Compile(`items[0]`, expr.Env(env))
	out, _ := expr.Run(program, env)
	fmt.Printf("  items[0] => %v\n", out) // 10

	program, _ = expr.Compile(`items[1:3]`, expr.Env(env))
	out, _ = expr.Run(program, env)
	fmt.Printf("  items[1:3] => %v\n", out) // [20 30]

	// Map field access via dot notation.
	program, _ = expr.Compile(`meta.version`, expr.Env(env))
	out, _ = expr.Run(program, env)
	fmt.Printf("  meta.version => %v\n", out) // 2

	// Nested expressions combining arrays and maps.
	program, _ = expr.Compile(`items[len(items)-1] + meta.version`, expr.Env(env))
	out, _ = expr.Run(program, env)
	fmt.Printf("  items[-1] + meta.version => %v\n", out) // 42
}

// ---------------------------------------------------------------------------
// 7. Custom helper functions in the environment
// ---------------------------------------------------------------------------

func customFunctions() {
	fmt.Println("\n=== 7. Custom functions ===")

	env := map[string]interface{}{
		"double": func(x float64) float64 { return x * 2 },
		"square": func(x float64) float64 { return x * x },
		"n":      5.0,
	}

	program, _ := expr.Compile(`double(square(n))`, expr.Env(env))
	out, _ := expr.Run(program, env)
	fmt.Printf("  double(square(5)) => %v\n", out) // 50

	// Functions with multiple arguments.
	env["clamp"] = func(val, min, max float64) float64 {
		if val < min {
			return min
		}
		if val > max {
			return max
		}
		return val
	}

	program, _ = expr.Compile(`clamp(150, 0, 100)`, expr.Env(env))
	out, _ = expr.Run(program, env)
	fmt.Printf("  clamp(150, 0, 100) => %v\n", out) // 100
}

// ---------------------------------------------------------------------------
// 8. Type checking at compile time
// ---------------------------------------------------------------------------

func compileTimeTypeCheck() {
	fmt.Println("\n=== 8. Compile-time type checking ===")

	env := map[string]interface{}{
		"name": "world", // string
	}

	// This compiles fine: string + string is valid.
	_, err := expr.Compile(`name + "!"`, expr.Env(env))
	fmt.Printf("  name + \"!\" compiled: %v\n", err == nil) // true

	// This fails at compile time: you cannot add a number to a string.
	_, err = expr.Compile(`name + 42`, expr.Env(env))
	fmt.Printf("  name + 42 compiled: %v\n", err) // compile error
}

// ---------------------------------------------------------------------------
// 9. let bindings (intermediate variables)
// ---------------------------------------------------------------------------

func letBindings() {
	fmt.Println("\n=== 9. let bindings ===")

	env := map[string]interface{}{
		"a": 10.0,
		"b": 3.0,
	}
	// let creates temporary variables within the expression.
	program, _ := expr.Compile(`let quot = a / b; quot * quot`, expr.Env(env))
	out, _ := expr.Run(program, env)
	fmt.Printf("  let quot = a / b; quot * quot => %v\n", out) // (10/3)² ≈ 11.111...
}

// ---------------------------------------------------------------------------
// 10. Nested struct environments (struct tags matter)
// ---------------------------------------------------------------------------

func structEnv() {
	fmt.Println("\n=== 10. Struct environment ===")

	type User struct {
		Name string
		Age  int
	}
	type AppContext struct {
		User  User
		Admin bool
		Items []string
	}

	ctx := AppContext{
		User:  User{Name: "Alice", Age: 30},
		Admin: true,
		Items: []string{"a", "b", "c"},
	}

	program, _ := expr.Compile(`User.Name + " is " + string(User.Age)`, expr.Env(ctx))
	out, _ := expr.Run(program, ctx)
	fmt.Printf("  User.Name + string(User.Age) => %v\n", out) // "Alice is 30"

	program, _ = expr.Compile(`Admin and len(Items) > 0`, expr.Env(ctx))
	out, _ = expr.Run(program, ctx)
	fmt.Printf("  Admin and len(Items) > 0 => %v\n", out) // true
}
