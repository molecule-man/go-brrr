# Plot compression ratio vs speed from a cross-library benchmark CSV.
#
# Usage: gnuplot -p scripts/plot-crosslib.gnuplot
#        gnuplot -e "csv='bench-crosslib.csv'" scripts/plot-crosslib.gnuplot
#        gnuplot -e "csv='bench-crosslib.csv'; outfile='plot.png'" scripts/plot-crosslib.gnuplot

if (!exists("csv")) csv = 'bench-crosslib.csv'

if (exists("outfile")) {
    if (outfile[strlen(outfile)-3:strlen(outfile)] eq '.svg') {
        set terminal svg size 600,600 enhanced background rgb 'white'
    } else {
        set terminal pngcairo size 600,600 enhanced
    }
    set output outfile
}

set datafile separator ','
set key top right
set xlabel 'Speed (MB/s)'
set ylabel 'Compression Ratio (higher = better)'
set grid

if (exists("plottitle")) { set title plottitle noenhanced font ',14' }

set autoscale fix
set offsets graph 0.04, graph 0.08, graph 0.06, graph 0.04

# A point gets a label if it is the lowest or highest level of its series, or if
# it is far enough from the last labeled point of the series.
mx(a, b) = a > b ? a : b
hi_b = hi_z = hi_g = -1
stats csv using ( \
    valid(2) ? (strcol(1) eq 'go-brrr' ? hi_b = mx(hi_b, $2) : \
                strcol(1) eq 'zstd'    ? hi_z = mx(hi_z, $2) : \
                strcol(1) eq 'gzip'    ? hi_g = mx(hi_g, $2) : 0) : 0, $3):4 nooutput
dx = 0.07 * (STATS_max_x - STATS_min_x)
dy = 0.05 * (STATS_max_y - STATS_min_y)
far(x, y) = abs(x - lx) > dx || abs(y - ly) > dy
pick(x, y, lvl, hi) = (far(x, y) || lvl == hi) ? (lx = x, ly = y, 1) : 0

color(lib) = lib eq 'go-brrr' ? 0x9400d3 : lib eq 'zstd' ? 0x2a8cc8 : 0xe69f00
row(lib) = sprintf("$0 > 0 && strcol(1) eq '%s' ? $4 : 1/0", lib)
series(lib, pt, prefix) = \
    sprintf("csv using 3:(%s) with linespoints pt %d ps 0.6 lc rgb %d title '%s', ", row(lib), pt, color(lib), lib) . \
    "lx = ly = -1e30, " . \
    sprintf("csv using 3:(%s):(pick($3, $4, $2, hi_%s) ? '%s'.strcol(2) : '') with labels offset char 0.9,0.6 font ',8' tc rgb %d notitle, ", row(lib), prefix, prefix, color(lib)) . \
    sprintf("csv using 3:(%s):(sprintf('%s %%s: %%s MB/s, ratio %%s', strcol(2), strcol(3), strcol(4))) with labels hypertext point pt 7 ps 1.5 lc rgb '#ff000000' notitle", row(lib), lib)

plotcmd = series('go-brrr', 7, 'b') . ", " . series('zstd', 9, 'z')
if (!exists("nogzip")) {
    plotcmd = plotcmd . ", " . series('gzip', 13, 'g')
}

eval('plot ' . plotcmd)

if (exists("outfile") && outfile[strlen(outfile)-3:strlen(outfile)] eq '.svg') {
    unset output
    system(sprintf("sed -i -e \"s/ onmousemove=\\\"gnuplot_svg.showHypertext(evt,'\\([^']*\\)')\\\" onmouseout=\\\"gnuplot_svg.hideHypertext()\\\"><title> <\\/title>/><title>\\1<\\/title>/\" '%s'", outfile))
}
