require('tsx/cjs/api').register();
const gulp = require('gulp');
const util = require('./lib/util');
const task = require('./lib/task');

const copyHtmlTask = task.define('copy-html', () => {
	return gulp.src('src/**/*.{css,html,js}').pipe(gulp.dest('out'));
})
gulp.task(copyHtmlTask);
// TODO: gulp-tsb
const watchClientTask = task.define('compile-client', task.series(util.rimraf('out'), copyHtmlTask));
gulp.task(watchClientTask);
