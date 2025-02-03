require('tsx/cjs/api').register();
const gulp = require('gulp');
const util = require('./lib/util');
const task = require('./lib/task');
const through2 = require('through2');

// https://github.com/gulpjs/gulp/issues/2790
const copyHtmlTask = task.define('copy-html', () => {
	return gulp.src('src/**/*.{css,html,js,ttf}', { encoding: false })
		.pipe(gulp.dest('out'));
})

gulp.task(copyHtmlTask);
// TODO: gulp-tsb
const watchClientTask = task.define('compile-client', task.series(util.rimraf('out'), copyHtmlTask));
gulp.task(watchClientTask);
