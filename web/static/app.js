// ========================================================================
// TokenMan Dashboard - Client-side Application
// ========================================================================

(function () {
    "use strict";

    // ---- Chart.js Initialisation ----
    let spendingChart = null;

    function initChart() {
        const ctx = document.getElementById("spending-chart");
        if (!ctx) return;

        spendingChart = new Chart(ctx, {
            type: "line",
            data: {
                labels: [],
                datasets: [
                    {
                        label: "Cost ($)",
                        data: [],
                        borderColor: "#ff6b6b",
                        backgroundColor: "rgba(255, 107, 107, 0.1)",
                        fill: true,
                        tension: 0.35,
                        pointRadius: 3,
                        pointHoverRadius: 5,
                        borderWidth: 2,
                    },
                    {
                        label: "Savings ($)",
                        data: [],
                        borderColor: "#00d4aa",
                        backgroundColor: "rgba(0, 212, 170, 0.1)",
                        fill: true,
                        tension: 0.35,
                        pointRadius: 3,
                        pointHoverRadius: 5,
                        borderWidth: 2,
                    },
                ],
            },
            options: {
                responsive: true,
                maintainAspectRatio: false,
                interaction: {
                    mode: "index",
                    intersect: false,
                },
                plugins: {
                    legend: {
                        position: "top",
                        labels: {
                            color: "#8892a4",
                            usePointStyle: true,
                            pointStyle: "circle",
                            padding: 16,
                            font: { size: 12 },
                        },
                    },
                    tooltip: {
                        backgroundColor: "#1a1a2e",
                        titleColor: "#e0e0e0",
                        bodyColor: "#8892a4",
                        borderColor: "#2a2a4a",
                        borderWidth: 1,
                        padding: 10,
                        callbacks: {
                            label: function (ctx) {
                                return ctx.dataset.label + ": " + formatCurrency(ctx.parsed.y);
                            },
                        },
                    },
                },
                scales: {
                    x: {
                        ticks: { color: "#5a6578", font: { size: 11 } },
                        grid: { color: "rgba(42, 42, 74, 0.3)" },
                    },
                    y: {
                        ticks: {
                            color: "#5a6578",
                            font: { size: 11 },
                            callback: function (val) {
                                return "$" + val.toFixed(2);
                            },
                        },
                        grid: { color: "rgba(42, 42, 74, 0.3)" },
                        beginAtZero: true,
                    },
                },
            },
        });
    }

    // ---- Formatting Helpers ----
    function formatCurrency(n) {
        if (n == null || isNaN(n)) return "$0.00";
        if (Math.abs(n) >= 1000) {
            return "$" + n.toFixed(0).replace(/\B(?=(\d{3})+(?!\d))/g, ",");
        }
        return "$" + n.toFixed(4);
    }

    function formatPercent(n) {
        if (n == null || isNaN(n)) return "0%";
        return n.toFixed(1) + "%";
    }

    function formatCompact(n) {
        if (n == null || isNaN(n)) return "0";
        if (n >= 1e9) return (n / 1e9).toFixed(1) + "B";
        if (n >= 1e6) return (n / 1e6).toFixed(1) + "M";
        if (n >= 1e3) return (n / 1e3).toFixed(1) + "K";
        return n.toLocaleString();
    }

    function formatTimestamp(ts) {
        if (!ts) return "--";
        try {
            var d = new Date(ts);
            if (isNaN(d.getTime())) return ts;
            return d.toLocaleString(undefined, {
                month: "short",
                day: "numeric",
                hour: "2-digit",
                minute: "2-digit",
                second: "2-digit",
            });
        } catch (_) {
            return ts;
        }
    }

    function setText(id, text) {
        var el = document.getElementById(id);
        if (el) el.textContent = text;
    }

    // ---- API Fetchers ----
    function fetchJSON(url) {
        return fetch(url)
            .then(function (r) {
                if (!r.ok) throw new Error("HTTP " + r.status);
                return r.json();
            })
            .catch(function (err) {
                console.error("Fetch error for " + url + ":", err);
                return null;
            });
    }

    // ---- Stats Update ----
    function updateStats() {
        fetchJSON("/api/stats").then(function (data) {
            if (!data) return;

            setText("uptime", data.uptime || "--");
            setText("total-savings", formatCurrency(data.savings_usd));
            setText("savings-percent", formatPercent(data.savings_percent) + " saved");
            setText("total-cost", formatCurrency(data.cost_usd));
            setText(
                "total-tokens",
                formatCompact(data.tokens_in + data.tokens_out) + " tokens"
            );
            setText("total-requests", formatCompact(data.total_requests));
            setText("active-requests", data.active_requests + " active");
            setText("cache-hit-rate", formatPercent(data.cache_hit_rate));
            setText(
                "cache-counts",
                formatCompact(data.cache_hits) + " hits / " + formatCompact(data.cache_misses) + " misses"
            );
        });
    }

    // ---- History Chart Update ----
    function updateHistory() {
        fetchJSON("/api/stats/history?range=30d").then(function (data) {
            if (!data || !spendingChart) return;

            var labels = data.map(function (p) {
                return p.timestamp;
            });
            var costs = data.map(function (p) {
                return p.cost;
            });
            var savings = data.map(function (p) {
                return p.savings;
            });

            spendingChart.data.labels = labels;
            spendingChart.data.datasets[0].data = costs;
            spendingChart.data.datasets[1].data = savings;
            spendingChart.update("none");
        });
    }

    // ---- Requests Table Update ----
    function updateRequests() {
        fetchJSON("/api/requests?page=1&limit=25").then(function (data) {
            if (!data || !data.requests) return;

            var tbody = document.getElementById("requests-body");
            if (!tbody) return;

            if (data.requests.length === 0) {
                tbody.innerHTML =
                    '<tr><td colspan="9" class="empty-state">No requests yet</td></tr>';
                return;
            }

            var html = "";
            data.requests.forEach(function (req) {
                var cacheTag = req.cache_hit
                    ? '<span class="tag tag-green">HIT</span>'
                    : '<span class="tag tag-muted">MISS</span>';

                var statusTag = "";
                if (req.status_code >= 200 && req.status_code < 300) {
                    statusTag = '<span class="tag tag-green">' + req.status_code + "</span>";
                } else if (req.status_code >= 400) {
                    statusTag = '<span class="tag tag-red">' + req.status_code + "</span>";
                } else {
                    statusTag = '<span class="tag tag-yellow">' + req.status_code + "</span>";
                }

                html += "<tr>";
                html += "<td>" + formatTimestamp(req.timestamp) + "</td>";
                html += "<td>" + escapeHtml(req.model) + "</td>";
                html += "<td>" + formatCompact(req.tokens_in) + "</td>";
                html += "<td>" + formatCompact(req.tokens_out) + "</td>";
                html += '<td class="cost-text">' + formatCurrency(req.cost_usd) + "</td>";
                html += '<td class="savings-text">' + formatCurrency(req.savings_usd) + "</td>";
                html += "<td>" + cacheTag + "</td>";
                html += "<td>" + req.latency_ms + "ms</td>";
                html += "<td>" + statusTag + "</td>";
                html += "</tr>";
            });

            tbody.innerHTML = html;
        });
    }

    // ---- Budget Update ----
    function updateBudget() {
        fetchJSON("/api/security/budget").then(function (data) {
            if (!data) return;

            data.forEach(function (b) {
                var prefix = "budget-" + b.period;
                var valEl = document.getElementById(prefix + "-value");
                var barEl = document.getElementById(prefix + "-bar");

                if (valEl) {
                    valEl.textContent =
                        formatCurrency(b.spent) + " / " + formatCurrency(b.limit);
                }
                if (barEl) {
                    var pct = Math.min(b.pct || 0, 100);
                    barEl.style.width = pct + "%";

                    // Change color based on usage.
                    barEl.className = "progress-fill";
                    if (pct >= 90) {
                        barEl.classList.add("progress-red");
                    } else if (pct >= 75) {
                        barEl.classList.add("progress-yellow");
                    } else if (b.period === "hourly") {
                        barEl.classList.add("progress-green");
                    } else if (b.period === "daily") {
                        barEl.classList.add("progress-blue");
                    } else {
                        barEl.classList.add("progress-purple");
                    }
                }
            });
        });
    }

    // ---- Providers Update ----
    function updateProviders() {
        fetchJSON("/api/providers").then(function (data) {
            if (!data) return;

            var container = document.getElementById("providers-list");
            if (!container) return;

            if (data.length === 0) {
                container.innerHTML =
                    '<div class="provider-placeholder">No providers configured</div>';
                return;
            }

            var html = "";
            data.forEach(function (p) {
                var statusClass = p.enabled ? "provider-enabled" : "provider-disabled";
                var statusText = p.enabled ? "Enabled" : "Disabled";

                html += '<div class="provider-item">';
                html += '<span class="provider-name">' + escapeHtml(p.name) + "</span>";
                html +=
                    '<span class="provider-status ' +
                    statusClass +
                    '">' +
                    statusText +
                    "</span>";
                html += "</div>";
            });

            container.innerHTML = html;
        });
    }

    // ---- PII Log Update ----
    function updatePIILog() {
        fetchJSON("/api/security/pii?page=1&limit=20").then(function (data) {
            if (!data || !data.entries) return;

            var tbody = document.getElementById("pii-body");
            if (!tbody) return;

            if (data.entries.length === 0) {
                tbody.innerHTML =
                    '<tr><td colspan="5" class="empty-state">No PII detections</td></tr>';
                return;
            }

            var html = "";
            data.entries.forEach(function (e) {
                html += "<tr>";
                html += "<td>" + formatTimestamp(e.timestamp) + "</td>";
                html += "<td>" + escapeHtml(e.request_id).substring(0, 8) + "...</td>";
                html += '<td><span class="tag tag-yellow">' + escapeHtml(e.pii_type) + "</span></td>";
                html += "<td>" + escapeHtml(e.action) + "</td>";
                html += "<td>" + escapeHtml(e.field_path) + "</td>";
                html += "</tr>";
            });

            tbody.innerHTML = html;
        });
    }

    // ---- Utility ----
    function escapeHtml(text) {
        if (!text) return "";
        var div = document.createElement("div");
        div.appendChild(document.createTextNode(text));
        return div.innerHTML;
    }

    // ---- Initialisation & Polling ----
    function init() {
        initChart();

        // Initial data load.
        updateStats();
        updateHistory();
        updateRequests();
        updateBudget();
        updateProviders();
        updatePIILog();

        // Polling intervals.
        setInterval(updateStats, 5000);
        setInterval(updateRequests, 10000);
        setInterval(updateHistory, 30000);
        setInterval(updateBudget, 15000);
        setInterval(updateProviders, 30000);
        setInterval(updatePIILog, 15000);
    }

    // Start when DOM is ready.
    if (document.readyState === "loading") {
        document.addEventListener("DOMContentLoaded", init);
    } else {
        init();
    }
})();
