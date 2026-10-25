/** An authenticator code (six digits) or a recovery code (sixteen of the
 * passcode alphabet, dashes optional), with spaces and dashes dropped and
 * letters upper-cased. The hub normalises the same way. */
export function compactSecondFactor(value: string): string {
  return value.replace(/[\s-]+/g, "").toUpperCase();
}

/** Whether a typed value has the shape of an authenticator or recovery code;
 * whether it is right is the hub's call. */
export function isSecondFactorCode(value: string): boolean {
  const code = compactSecondFactor(value);
  return /^\d{6}$/.test(code) || /^[A-Z2-9]{16}$/.test(code);
}
