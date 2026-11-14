// Package finalpath resolves a folder to the one the operating system
// finally opens: symbolic links and, on Windows, junctions, \\?\ and \\.\
// prefixes, 8.3 short names and letter case. Guards that compare folders
// (which ones agents may work in, which hold Nodevas's own data) compare
// what this returns, since a path that merely looks different can name the
// same folder.
package finalpath
