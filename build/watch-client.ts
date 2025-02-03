import gulp from 'gulp';
import ts from 'gulp-typescript';
import rimraf from 'rimraf';



const tsp = ts.createProject('src/tsconfig.json');

gulp.task('tsb', () => {
	return gulp.src('src/**/*.ts').pipe(tsp()).pipe(gulp.dest('out'));
});

gulp.task('clean-out', (done) => {
	rimraf('out', e => {
		if (e) {
			console.error(e);
			return;
		}
		done();
	});
});

gulp.task('default', gulp.series('clean-out', gulp.parallel('tsb', 'copy-html')));
