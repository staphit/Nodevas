/**
 * `requires` expression helpers [A-04].
 *
 * The expression on a node is the truth; edges are its visual cache. These
 * helpers are shared by the canvas (which shows the operator on the gate glyph)
 * and by the commands that edit dependencies.
 */

/** Operators a user can pick when combining conditions on the canvas. */
export const EDITABLE_GATE_OPERATORS = new Set(["and", "or", "xor", "nand", "nor"]);

/**
 * Top-level boolean operator of a requires expression (label shown on the
 * canvas). Scans outside parentheses; the loosest-binding operator present
 * is the top-level one (or/nor < xor < and/nand).
 */
export function topLevelOp(expr: string | undefined): string | null {
  if (!expr) return null;
  let depth = 0;
  const found = new Set<string>();
  const re = /\b(and|or|not|xor|nand|nor)\b|[()]/gi;
  let m: RegExpExecArray | null;
  while ((m = re.exec(expr))) {
    const tok = m[0];
    if (tok === "(") depth++;
    else if (tok === ")") depth--;
    else if (depth === 0) found.add(tok.toLowerCase());
  }
  // Binary operators define the top-level relationship. Unary NOT is only
  // returned when no top-level binary operator exists.
  for (const op of ["or", "nor", "xor", "nand", "and", "not"]) {
    if (found.has(op)) return op;
  }
  return null;
}

/** Falls back to `and` for expressions the canvas editor cannot represent. */
export function editableGateOperator(value: string | null): string {
  return value && EDITABLE_GATE_OPERATORS.has(value) ? value : "and";
}

/**
 * Appends one node reference to an expression, parenthesising the existing
 * expression when the operator changes so precedence cannot shift silently.
 */
export function appendRequirement(
  expression: string,
  reference: string,
  operator: string,
): string {
  const existing = expression.trim();
  if (!existing) return reference;
  const existingOperator = topLevelOp(existing);
  const left =
    existingOperator && existingOperator !== operator ? `(${existing})` : existing;
  return `${left} ${operator} ${reference}`;
}

// Runtime evaluator. This deliberately mirrors internal/engine/dsl rather
// than treating the edge projection as executable state: agents and the Web
// analyzer must answer the same truth table even when an old graph's wires
// have not been repaired yet.
type TokenKind = "ident" | "number" | "string" | "(" | ")" | "," | "cmp" | "eof";

interface Token {
  kind: TokenKind;
  text: string;
}

type Literal =
  | { kind: "number"; value: number }
  | { kind: "boolean"; value: boolean }
  | { kind: "string"; value: string };

type RequiresExpr =
  | { kind: "node"; id: string }
  | { kind: "flag"; name: string; op?: string; literal?: Literal }
  | { kind: "not"; value: RequiresExpr }
  | { kind: "binary"; op: string; left: RequiresExpr; right: RequiresExpr };

export interface RequiresEvaluation {
  /** False means the expression did not parse and therefore failed closed. */
  ok: boolean;
  satisfied: boolean;
  nodeRefs: string[];
  /** Node ids that best explain a false condition; flags can yield none. */
  blockedBy: string[];
}

const KEYWORDS = new Set([
  "and",
  "or",
  "xor",
  "nand",
  "nor",
  "not",
  "all",
  "any",
  "flag",
  "true",
  "false",
]);

function identStart(char: string): boolean {
  return /[A-Za-z_]/.test(char);
}

function identPart(char: string): boolean {
  return /[A-Za-z0-9_.-]/.test(char);
}

function lexRequires(source: string): Token[] {
  const tokens: Token[] = [];
  let index = 0;
  while (index < source.length) {
    const char = source[index];
    if (" \t\r\n".includes(char)) {
      index += 1;
      continue;
    }
    if (char === "(" || char === ")" || char === ",") {
      tokens.push({ kind: char, text: char });
      index += 1;
      continue;
    }
    if ("=!><".includes(char)) {
      const pair = source.slice(index, index + 2);
      if (["==", "!=", ">=", "<="].includes(pair)) {
        tokens.push({ kind: "cmp", text: pair });
        index += 2;
      } else if (char === "=" || char === ">" || char === "<") {
        tokens.push({ kind: "cmp", text: char === "=" ? "==" : char });
        index += 1;
      } else {
        throw new Error("invalid comparison");
      }
      continue;
    }
    if (char === '"' || char === "'") {
      const quote = char;
      const start = ++index;
      while (index < source.length && source[index] !== quote) index += 1;
      if (index >= source.length) throw new Error("unterminated string");
      tokens.push({ kind: "string", text: source.slice(start, index) });
      index += 1;
      continue;
    }
    if (/\d/.test(char) || (char === "-" && /\d/.test(source[index + 1] ?? ""))) {
      const start = index;
      if (char === "-") index += 1;
      while (index < source.length && /[\d.]/.test(source[index])) index += 1;
      tokens.push({ kind: "number", text: source.slice(start, index) });
      continue;
    }
    if (identStart(char)) {
      const start = index++;
      while (index < source.length && identPart(source[index])) index += 1;
      tokens.push({ kind: "ident", text: source.slice(start, index) });
      continue;
    }
    throw new Error("invalid character");
  }
  tokens.push({ kind: "eof", text: "" });
  return tokens;
}

function precedence(operator: string): number {
  if (operator === "or" || operator === "nor") return 1;
  if (operator === "xor") return 2;
  if (operator === "and" || operator === "nand") return 3;
  return 0;
}

class RequiresParser {
  private index = 0;

  constructor(private readonly tokens: Token[]) {}

  parse(): RequiresExpr {
    const expression = this.parseExpression(0);
    if (this.peek().kind !== "eof") throw new Error("trailing token");
    return expression;
  }

  private peek(): Token {
    return this.tokens[this.index];
  }

  private next(): Token {
    return this.tokens[this.index++];
  }

  private expect(kind: TokenKind): Token {
    const token = this.next();
    if (token.kind !== kind) throw new Error(`expected ${kind}`);
    return token;
  }

  private parseExpression(minimum: number): RequiresExpr {
    let left = this.parseUnary();
    for (;;) {
      const token = this.peek();
      if (token.kind !== "ident") return left;
      const operator = token.text.toLowerCase();
      const rank = precedence(operator);
      if (rank === 0 || rank < minimum) return left;
      this.next();
      const right = this.parseExpression(rank + 1);
      left = { kind: "binary", op: operator, left, right };
    }
  }

  private parseUnary(): RequiresExpr {
    const token = this.peek();
    if (token.kind === "ident" && token.text.toLowerCase() === "not") {
      this.next();
      return { kind: "not", value: this.parseUnary() };
    }
    return this.parsePrimary();
  }

  private parsePrimary(): RequiresExpr {
    const token = this.next();
    if (token.kind === "(") {
      const expression = this.parseExpression(0);
      this.expect(")");
      return expression;
    }
    if (token.kind !== "ident") throw new Error("expected expression");
    const keyword = token.text.toLowerCase();
    if (keyword === "flag") return this.parseFlag();
    if (keyword === "all" || keyword === "any") return this.parseList(keyword);
    if (KEYWORDS.has(keyword)) throw new Error("unexpected keyword");
    return { kind: "node", id: token.text };
  }

  private parseFlag(): RequiresExpr {
    this.expect("(");
    const name = this.expect("ident").text;
    if (KEYWORDS.has(name.toLowerCase())) throw new Error("keyword flag name");
    let op: string | undefined;
    let literal: Literal | undefined;
    if (this.peek().kind === "cmp") {
      op = this.next().text;
      literal = this.parseLiteral();
    }
    this.expect(")");
    return { kind: "flag", name, op, literal };
  }

  private parseLiteral(): Literal {
    const token = this.next();
    if (token.kind === "number") {
      const value = Number(token.text);
      if (!Number.isFinite(value)) throw new Error("invalid number");
      return { kind: "number", value };
    }
    if (token.kind === "string") return { kind: "string", value: token.text };
    if (token.kind === "ident") {
      const value = token.text.toLowerCase();
      if (value === "true" || value === "false") {
        return { kind: "boolean", value: value === "true" };
      }
      return { kind: "string", value: token.text };
    }
    throw new Error("expected literal");
  }

  private parseList(which: "all" | "any"): RequiresExpr {
    this.expect("(");
    let expression = this.parseExpression(0);
    while (this.nextListSeparator()) {
      expression = {
        kind: "binary",
        op: which === "all" ? "and" : "or",
        left: expression,
        right: this.parseExpression(0),
      };
    }
    return expression;
  }

  /** Returns true for a comma and false after consuming the closing paren. */
  private nextListSeparator(): boolean {
    const token = this.next();
    if (token.kind === ")") return false;
    if (token.kind !== ",") throw new Error("expected list separator");
    return true;
  }
}

function evaluateFlag(
  expression: Extract<RequiresExpr, { kind: "flag" }>,
  flags: Record<string, unknown>,
): boolean {
  const present = Object.prototype.hasOwnProperty.call(flags, expression.name);
  const value = flags[expression.name];
  if (!expression.op) {
    if (!present || value == null) return false;
    if (typeof value === "boolean") return value;
    if (typeof value === "number") return value !== 0;
    if (typeof value === "string") return value !== "";
    return true;
  }
  if (!present || !expression.literal) return false;
  const literal = expression.literal;
  if (literal.kind === "number") {
    if (typeof value !== "number") return false;
    if (expression.op === "==") return value === literal.value;
    if (expression.op === "!=") return value !== literal.value;
    if (expression.op === ">=") return value >= literal.value;
    if (expression.op === "<=") return value <= literal.value;
    if (expression.op === ">") return value > literal.value;
    if (expression.op === "<") return value < literal.value;
    return false;
  }
  if (literal.kind === "boolean") {
    if (typeof value !== "boolean") return false;
    if (expression.op === "==") return value === literal.value;
    if (expression.op === "!=") return value !== literal.value;
    return false;
  }
  if (typeof value !== "string") return false;
  if (expression.op === "==") return value === literal.value;
  if (expression.op === "!=") return value !== literal.value;
  return false;
}

function evalExpression(
  expression: RequiresExpr,
  done: ReadonlySet<string>,
  flags: Record<string, unknown>,
): boolean {
  if (expression.kind === "node") return done.has(expression.id);
  if (expression.kind === "flag") return evaluateFlag(expression, flags);
  if (expression.kind === "not") return !evalExpression(expression.value, done, flags);
  const left = evalExpression(expression.left, done, flags);
  const right = evalExpression(expression.right, done, flags);
  if (expression.op === "and") return left && right;
  if (expression.op === "or") return left || right;
  if (expression.op === "xor") return left !== right;
  if (expression.op === "nand") return !(left && right);
  if (expression.op === "nor") return !(left || right);
  return false;
}

function nodeRefs(expression: RequiresExpr): string[] {
  const refs: string[] = [];
  const seen = new Set<string>();
  const visit = (current: RequiresExpr) => {
    if (current.kind === "node") {
      if (!seen.has(current.id)) {
        seen.add(current.id);
        refs.push(current.id);
      }
      return;
    }
    if (current.kind === "not") visit(current.value);
    if (current.kind === "binary") {
      visit(current.left);
      visit(current.right);
    }
  };
  visit(expression);
  return refs;
}

function falseBlockers(expression: RequiresExpr, done: ReadonlySet<string>): string[] {
  const refs = nodeRefs(expression);
  const completed = refs.filter((id) => done.has(id));
  const pending = refs.filter((id) => !done.has(id));
  if (expression.kind === "not") return completed.length ? completed : refs;
  if (expression.kind === "binary") {
    if (expression.op === "xor" && completed.length > 1) return completed;
    if (expression.op === "nand" || expression.op === "nor") {
      return completed.length ? completed : refs;
    }
  }
  return pending.length ? pending : completed;
}

/** Evaluates exactly the DSL accepted by Go; syntax errors fail closed. */
export function evaluateRequires(
  source: string | undefined,
  done: ReadonlySet<string>,
  flags: Record<string, unknown> = {},
): RequiresEvaluation {
  if (!source?.trim()) return { ok: true, satisfied: true, nodeRefs: [], blockedBy: [] };
  try {
    const expression = new RequiresParser(lexRequires(source)).parse();
    const refs = nodeRefs(expression);
    const satisfied = evalExpression(expression, done, flags);
    return {
      ok: true,
      satisfied,
      nodeRefs: refs,
      blockedBy: satisfied ? [] : falseBlockers(expression, done),
    };
  } catch {
    return { ok: false, satisfied: false, nodeRefs: [], blockedBy: [] };
  }
}
