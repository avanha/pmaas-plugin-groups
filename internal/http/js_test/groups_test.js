"use strict";
// Tests for content/static/js/groups.js. Run with: node js_test/groups_test.js (the Go test in oldbrowsers_test.go does).
var assert = require("assert");
var fs = require("fs");
var groups = require("../content/static/js/groups.js");

var tests = [];
function test(name, fn) { tests.push([name, fn]); }

function item(className) { return {className: className || ""}; }
function doc(items) {
    return {querySelectorAll: function () { return items.map(function (i) { return {parentNode: i}; }); }};
}

test("each item that holds a divider is marked, and only once", function () {
    var a = item(), b = item("other");
    var d = doc([a, b]);

    assert.strictEqual(groups.markDividers(d), 2);
    assert.strictEqual(a.className, "group-divider-item");
    assert.strictEqual(b.className, "other group-divider-item");

    assert.strictEqual(groups.markDividers(d), 0);
    assert.strictEqual(b.className, "other group-divider-item");
});

test("nothing to mark is nothing marked", function () {
    assert.strictEqual(groups.markDividers(doc([])), 0);
});

test("the script uses nothing Safari 9 does not have", function () {
    var source = fs.readFileSync(__dirname + "/../content/static/js/groups.js", "utf8").replace(/\/\*[\s\S]*?\*\//g, "").replace(/\/\/.*$/gm, "");

    [
        [/=>/, "an arrow function"], [/\bclass\s+\w/, "a class"], [/\blet\s/, "let"], [/\bconst\s/, "const"], [/`/, "a template literal"],
        [/\basync\b/, "async"], [/\bawait\b/, "await"], [/\bfetch\s*\(/, "fetch"], [/\.\.\./, "spread"], [/\?\./, "optional chaining"],
        [/\w+\.forEach/, "forEach (a NodeList has none before Safari 10)"]
    ].forEach(function (pair) {
        assert.ok(!pair[0].test(source), "the script has " + pair[1]);
    });
});

var failed = 0;
tests.forEach(function (t) {
    try { t[1](); console.log("ok   " + t[0]); } catch (e) { failed++; console.log("FAIL " + t[0] + "\n     " + (e && e.stack || e)); }
});
if (failed) { process.exit(1); }
