/*
 * Marks the list items that hold a heading (the kind of the entities after it), so that the CSS can give the whole row to it: see
 * .group-divider-item in groups.css. A browser that has :has() needs none of this, and does not mind it. It is for the ones that do not (Safari
 * before 15.4, an iPad that stopped at iOS 9 among them), where a heading would be a card with entities beside it.
 *
 * Plain ES5, for the same old Safari: no arrow functions, let or const, and no forEach on a NodeList, which has none before Safari 10.
 */
(function (global) {
    "use strict";

    // markDividers puts the class on the parent of each divider that is in a list of entities, and says how many it marked.
    function markDividers(doc) {
        var dividers = doc.querySelectorAll("ol.entity-list li > .group-divider");
        var count = 0;
        var i;

        for (i = 0; i < dividers.length; i++) {
            var item = dividers[i].parentNode;

            if ((" " + item.className + " ").indexOf(" group-divider-item ") < 0) {
                item.className = item.className ? item.className + " group-divider-item" : "group-divider-item";
                count++;
            }
        }

        return count;
    }

    if (typeof module !== "undefined" && module.exports) {
        module.exports = {markDividers: markDividers};
    }

    if (typeof document !== "undefined" && global.addEventListener) {
        var run = function () {
            markDividers(document);
        };

        if (document.readyState === "loading") {
            document.addEventListener("DOMContentLoaded", run, false);
        } else {
            run();
        }
    }
})(typeof window !== "undefined" ? window : globalThis);
